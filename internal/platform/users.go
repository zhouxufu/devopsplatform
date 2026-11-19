package platform

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin             = "admin"
	RoleMember            = "member"
	minimumPasswordLength = 6
)

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{1,63}$`)

// User is deliberately small: the platform has two built-in roles only.
// PasswordHash is carried by the database migration snapshot but never exposed
// by JSON APIs.
type User struct {
	Duties             []string `json:"duties"`
	Username           string   `json:"username"`
	DisplayName        string   `json:"display_name"`
	Role               string   `json:"role"`
	PasswordHash       string   `json:"-"`
	Enabled            bool     `json:"enabled"`
	CreatedAt          int64    `json:"created_at"`
	UpdatedAt          int64    `json:"updated_at"`
	Source             string   `json:"source,omitempty"`
	PasswordConfigured bool     `json:"password_configured"`
}

func normalizeUser(user User) (User, error) {
	var err error
	if user.Duties, err = normalizeDuties(user.Duties); err != nil {
		return user, err
	}
	user.Username = strings.TrimSpace(user.Username)
	user.DisplayName = strings.TrimSpace(user.DisplayName)
	user.Role = strings.ToLower(strings.TrimSpace(user.Role))
	if !usernameRE.MatchString(user.Username) {
		return user, fmt.Errorf("用户名长度需为 2-64 位，仅支持字母、数字、点、短横线和下划线")
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	if user.Role != RoleAdmin && user.Role != RoleMember {
		return user, fmt.Errorf("角色仅支持 admin 或 member")
	}
	return user, nil
}

func (r *Registry) usersLocked() ([]User, error) {
	rows, err := r.db.Query(`SELECT u.username,u.display_name,u.role,u.password_hash,u.enabled,u.created_at,u.updated_at,COALESCE(i.source,'') FROM platform_users u LEFT JOIN platform_user_identities i ON i.username=u.username ORDER BY u.role,u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Username, &user.DisplayName, &user.Role, &user.PasswordHash, &user.Enabled, &user.CreatedAt, &user.UpdatedAt, &user.Source); err != nil {
			return nil, err
		}
		user.PasswordConfigured = user.PasswordHash != ""
		users = append(users, user)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		for i := range users {
			users[i].Duties = r.userDuties(users[i].Username)
		}
	}
	return users, err
}

func (r *Registry) Users() ([]User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usersLocked()
}

func (r *Registry) EnsureBootstrapAdmin(password string) error {
	if password == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM platform_users WHERE role='admin'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM platform_users WHERE username='admin'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("admin 用户名已被占用，请先配置平台管理员")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = r.db.Exec(`INSERT INTO platform_users(username,display_name,role,password_hash,enabled,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`, "admin", "管理员", RoleAdmin, string(hash), now, now)
	return err
}

func (r *Registry) Authenticate(username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	var user User
	err := r.db.QueryRow(`SELECT username,display_name,role,password_hash,enabled,created_at,updated_at FROM platform_users WHERE username=?`, username).
		Scan(&user.Username, &user.DisplayName, &user.Role, &user.PasswordHash, &user.Enabled, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("用户名或密码错误")
	}
	if err != nil {
		return nil, err
	}
	if !user.Enabled || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, fmt.Errorf("用户名或密码错误")
	}
	return &user, nil
}

// SaveUser creates or updates one account. Empty password keeps the existing
// hash, which makes ordinary role/name edits safe.
func (r *Registry) SaveUser(user User, password string) (*User, error) {
	var err error
	if user, err = normalizeUser(user); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var existing User
	err = r.db.QueryRow(`SELECT username,display_name,role,password_hash,enabled,created_at,updated_at FROM platform_users WHERE username=?`, user.Username).
		Scan(&existing.Username, &existing.DisplayName, &existing.Role, &existing.PasswordHash, &existing.Enabled, &existing.CreatedAt, &existing.UpdatedAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == sql.ErrNoRows && utf8.RuneCountInString(password) < minimumPasswordLength {
		return nil, fmt.Errorf("新用户密码至少 6 位")
	}
	hash := existing.PasswordHash
	if password != "" {
		if utf8.RuneCountInString(password) < minimumPasswordLength {
			return nil, fmt.Errorf("密码至少 6 位")
		}
		encoded, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return nil, hashErr
		}
		hash = string(encoded)
	}
	if user.Enabled && hash == "" {
		return nil, fmt.Errorf("请先设置平台密码，再启用同步成员")
	}
	now, createdAt := time.Now().Unix(), existing.CreatedAt
	if createdAt == 0 {
		createdAt = now
	}
	query := `INSERT INTO platform_users(username,display_name,role,password_hash,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(username) DO UPDATE SET display_name=excluded.display_name,role=excluded.role,password_hash=excluded.password_hash,enabled=excluded.enabled,updated_at=excluded.updated_at`
	if r.dialect == "mysql" {
		query = `INSERT INTO platform_users(username,display_name,role,password_hash,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE display_name=VALUES(display_name),role=VALUES(role),password_hash=VALUES(password_hash),enabled=VALUES(enabled),updated_at=VALUES(updated_at)`
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(query, user.Username, user.DisplayName, user.Role, hash, user.Enabled, createdAt, now); err != nil {
		return nil, err
	}
	if user.Duties != nil {
		raw, _ := json.Marshal(user.Duties)
		if _, err = tx.Exec(`DELETE FROM platform_user_duties WHERE username=?`, user.Username); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO platform_user_duties(username,document) VALUES(?,?)`, user.Username, string(raw)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	user.Duties = r.userDuties(user.Username)
	user.PasswordHash, user.CreatedAt, user.UpdatedAt = hash, createdAt, now
	user.PasswordConfigured = hash != ""
	return &user, nil
}

func (r *Registry) DeleteUser(username, current string) error {
	username = strings.TrimSpace(username)
	if username == current {
		return fmt.Errorf("不能删除当前登录账号")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var role string
	if err := r.db.QueryRow(`SELECT role FROM platform_users WHERE username=?`, username).Scan(&role); err == sql.ErrNoRows {
		return fmt.Errorf("用户不存在")
	} else if err != nil {
		return err
	}
	if role == RoleAdmin {
		var admins int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM platform_users WHERE role=? AND enabled=1`, RoleAdmin).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("至少保留一个启用的管理员")
		}
	}
	_, err := r.db.Exec(`DELETE FROM platform_users WHERE username=?`, username)
	return err
}
