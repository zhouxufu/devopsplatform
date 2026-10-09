package deploy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var attachmentIDRE = regexp.MustCompile(`^[a-f0-9]{32}$`)
var attachmentReferenceRE = regexp.MustCompile(`attachment:([a-f0-9]{32})`)

const maxWorkImage = 8 << 20

func (s *Store) migrateWorkContent() error {
	// Portable metadata; files stay in the platform's persistent data directory.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS work_item_attachments (id VARCHAR(32) PRIMARY KEY, owner VARCHAR(191) NOT NULL, name TEXT NOT NULL, mime VARCHAR(64) NOT NULL, size INTEGER NOT NULL, work_item_id BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL)`); err != nil {
		return err
	}
	if s.db.dialect == "mysql" {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='work_items' AND COLUMN_NAME='description_format'`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			_, err := s.db.Exec(`ALTER TABLE work_items ADD COLUMN description_format VARCHAR(16) NOT NULL DEFAULT ''`)
			return err
		}
	} else if _, err := s.db.Exec(`SELECT description_format FROM work_items LIMIT 1`); err != nil {
		_, err = s.db.Exec(`ALTER TABLE work_items ADD COLUMN description_format TEXT NOT NULL DEFAULT ''`)
		return err
	}
	return nil
}

func bindWorkAttachments(tx *deployTx, item *WorkItem, actor string) error {
	seen := map[string]bool{}
	for _, m := range attachmentReferenceRE.FindAllStringSubmatch(item.Description, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(seen) > 30 {
			return fmt.Errorf("每条需求最多关联 30 张图片")
		}
		var owner string
		var bound int64
		q := `SELECT owner,work_item_id FROM work_item_attachments WHERE id=?`
		if tx.dialect == "mysql" {
			q += " FOR UPDATE"
		}
		if err := tx.QueryRow(q, id).Scan(&owner, &bound); err != nil {
			return fmt.Errorf("说明中包含不存在的图片")
		}
		if (bound != 0 && bound != item.ID) || (bound == 0 && owner != actor) {
			return fmt.Errorf("图片不属于当前草稿或需求")
		}
		if _, err := tx.Exec(`UPDATE work_item_attachments SET work_item_id=? WHERE id=?`, item.ID, id); err != nil {
			return err
		}
	}
	return nil
}

func registerWorkItemAttachmentRoutes(mux *http.ServeMux, s *Store) {
	actor := func(r *http.Request) string {
		v := strings.TrimSpace(r.Header.Get("X-User"))
		if v == "" {
			v = "local"
		}
		return v
	}
	fail := func(w http.ResponseWriter, status int, message string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
	mux.HandleFunc("POST /api/work-item-attachments", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxWorkImage+(64<<10))
		if err := r.ParseMultipartForm(maxWorkImage + (64 << 10)); err != nil {
			fail(w, 400, "图片过大或上传格式错误（单张最多 8 MiB）")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			fail(w, 400, "请选择图片")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxWorkImage+1))
		if err != nil || len(data) > maxWorkImage {
			fail(w, 400, "单张图片最多 8 MiB")
			return
		}
		cfg, kind, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
			fail(w, 400, "仅支持 PNG、JPEG、GIF 图片，最多 4000 万像素")
			return
		}
		mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[kind]
		if mime == "" {
			fail(w, 400, "不支持该图片格式")
			return
		}
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM work_item_attachments WHERE owner=? AND work_item_id=0`, actor(r)).Scan(&count); err != nil {
			fail(w, 500, "无法检查附件配额")
			return
		}
		if count >= 200 {
			fail(w, 400, "未关联图片达到 200 张，请先保存已有草稿")
			return
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			fail(w, 500, "无法创建附件")
			return
		}
		id := hex.EncodeToString(random[:])
		dir := filepath.Join(s.dataDir, "work-item-attachments")
		if err = os.MkdirAll(dir, 0700); err != nil {
			fail(w, 500, "无法保存附件")
			return
		}
		path := filepath.Join(dir, id)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			fail(w, 500, "无法保存附件")
			return
		}
		_, err = f.Write(data)
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(path)
			fail(w, 500, "无法保存附件")
			return
		}
		name := filepath.Base(header.Filename)
		if len(name) > 255 {
			name = "image"
		}
		if _, err = s.db.Exec(`INSERT INTO work_item_attachments(id,owner,name,mime,size,created_at) VALUES(?,?,?,?,?,?)`, id, actor(r), name, mime, len(data), time.Now().Unix()); err != nil {
			os.Remove(path)
			fail(w, 500, "无法登记附件")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": id, "name": name, "reference": "attachment:" + id, "size": len(data)})
	})
	mux.HandleFunc("GET /api/work-item-attachments/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !attachmentIDRE.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		var owner, mime string
		var bound int64
		if err := s.db.QueryRow(`SELECT owner,mime,work_item_id FROM work_item_attachments WHERE id=?`, id).Scan(&owner, &mime, &bound); err != nil {
			http.NotFound(w, r)
			return
		}
		if bound == 0 && actor(r) != owner && r.Header.Get("X-Role") != "admin" {
			fail(w, 403, "未保存的图片仅上传者可查看")
			return
		}
		if bound != 0 && s.GetWorkItem(bound) == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeFile(w, r, filepath.Join(s.dataDir, "work-item-attachments", id))
	})
}
