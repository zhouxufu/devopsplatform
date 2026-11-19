package monitor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Credentials are separate from public target metadata. Never migrate temporary
// console passwords into this table: saving requires explicit configuration.
const arthasCredentialDDL = `CREATE TABLE IF NOT EXISTS arthas_credentials (target_id VARCHAR(64) PRIMARY KEY, encrypted TEXT NOT NULL)`

type arthasCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Store) arthasKey(create bool) ([]byte, error) {
	s.arthasKeyMu.Lock()
	defer s.arthasKeyMu.Unlock()
	path := filepath.Join(s.arthasSecretDir, "credentials.key")
	if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("凭据密钥不可使用符号链接")
	}
	if info, e := os.Lstat(s.arthasSecretDir); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("凭据目录不可使用符号链接")
	}
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		var existing int
		if s.db.QueryRow(`SELECT COUNT(*) FROM arthas_credentials`).Scan(&existing) != nil || existing > 0 {
			return nil, errors.New("已有认证但本机密钥缺失，请恢复原 credentials.key；不会生成替代密钥")
		}
		if err = os.MkdirAll(s.arthasSecretDir, 0o700); err != nil {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			key, err = os.ReadFile(path)
		} else if openErr != nil {
			return nil, openErr
		} else {
			_, err = f.Write(key)
			if err == nil {
				err = f.Sync()
			}
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("Arthas 凭据密钥不可用，请恢复原密钥；不会重置已有凭据")
	}
	if err = os.Chmod(path, 0o600); err != nil {
		return nil, errors.New("凭据密钥权限不可用")
	}
	return key, nil
}

func arthasCredentialBinding(t *arthasTarget) []byte {
	return []byte(t.ID + "\x00" + t.Endpoint + "\x00" + t.Service + "\x00" + t.Environment)
}

func (s *Store) sealArthasCredential(t *arthasTarget, c *arthasCredential) (string, error) {
	key, err := s.arthasKey(true)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, arthasCredentialBinding(t))), nil
}

func (s *Store) savedArthasCredential(t *arthasTarget) (*arthasCredential, error) {
	var encrypted string
	err := s.db.QueryRow(`SELECT encrypted FROM arthas_credentials WHERE target_id=?`, t.ID).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("读取 Arthas 认证配置失败")
	}
	key, err := s.arthasKey(false)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("Arthas 凭据不可用")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("Arthas 凭据不可用")
	}
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(raw) < gcm.NonceSize() {
		return nil, errors.New("Arthas 凭据损坏，请重新配置")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], arthasCredentialBinding(t))
	if err != nil {
		return nil, errors.New("Arthas 凭据与当前目标不匹配，请重新配置")
	}
	var c arthasCredential
	if json.Unmarshal(plain, &c) != nil {
		return nil, errors.New("Arthas 凭据损坏，请重新配置")
	}
	return &c, nil
}
