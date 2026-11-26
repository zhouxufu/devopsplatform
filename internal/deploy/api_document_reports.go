package deploy

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/personnames"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type DocumentReport struct {
	Association      *DocumentReportLinks `json:"association,omitempty"`
	AssociationError string               `json:"association_error,omitempty"`
	Number           int64                `json:"number"`
	ID               string               `json:"id"`
	ApplicationID    string               `json:"application_id"`
	Service          string               `json:"service"`
	Environment      string               `json:"environment"`
	Protocol         string               `json:"protocol"`
	Actor            string               `json:"actor"`
	ActorName        string               `json:"actor_name,omitempty"`
	Source           string               `json:"source"`
	State            string               `json:"state"`
	CreatedAt        int64                `json:"created_at"`
	UpdatedAt        int64                `json:"updated_at"`
	ExpiresAt        int64                `json:"expires_at,omitempty"`
	SnapshotID       int64                `json:"snapshot_id"`
	BaselineID       int64                `json:"baseline_id"`
	CurrentID        int64                `json:"current_id"`
	Changes          apidoc.ChangeSummary `json:"changes"`
	Error            string               `json:"error,omitempty"`
	RunID            int64                `json:"run_id,omitempty"`
	Commit           string               `json:"commit,omitempty"`
	RequestDigest    string               `json:"-"`
}

const reportColumns = `id,application_id,service,environment,protocol,actor,source,state,created_at,updated_at,expires_at,snapshot_id,baseline_id,summary,error,run_id,commit_ref,request_digest`

func documentReportSchema(doc string) string {
	identity := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if doc == "LONGTEXT" {
		identity = "BIGINT PRIMARY KEY AUTO_INCREMENT"
	}
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS api_document_reports (sequence %s,id VARCHAR(64) NOT NULL UNIQUE,application_id VARCHAR(191) NOT NULL,service VARCHAR(191) NOT NULL,environment VARCHAR(32) NOT NULL,protocol VARCHAR(16) NOT NULL,actor VARCHAR(191) NOT NULL,source VARCHAR(32) NOT NULL,state VARCHAR(32) NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,expires_at BIGINT NOT NULL,snapshot_id BIGINT NOT NULL,baseline_id BIGINT NOT NULL,summary %s NOT NULL,error TEXT NOT NULL,run_id BIGINT NOT NULL,commit_ref VARCHAR(191) NOT NULL,request_digest VARCHAR(80) NOT NULL)`, identity, doc)
}

func (s *Store) ensureDocumentReportIndexes() error {
	for name, columns := range map[string]string{"idx_document_reports_scope": "application_id,environment,protocol,created_at", "idx_document_reports_snapshot": "snapshot_id,state", "idx_document_reports_time": "created_at,id"} {
		if s.Driver() == "mysql" {
			var count int
			if e := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_document_reports' AND INDEX_NAME=?`, name).Scan(&count); e != nil {
				return e
			}
			if count > 0 {
				continue
			}
			if _, e := s.db.Exec("CREATE INDEX " + name + " ON api_document_reports(" + columns + ")"); e != nil {
				return e
			}
		} else {
			if _, e := s.db.Exec("CREATE INDEX IF NOT EXISTS " + name + " ON api_document_reports(" + columns + ")"); e != nil {
				return e
			}
		}
	}
	return nil
}

func newDocumentReportID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

const reportSelectColumns = reportColumns + ",sequence"

var reportIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Compare semantic submissions without losing large JSON numbers. Resolved
// requirement labels may be enriched by preview; their IDs remain the identity.
func documentReportFingerprint(raw []byte) string {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	doc := value["document"]
	if doc == nil {
		doc = value
	}
	context := map[string]any{}
	if c, ok := value["change_context"].(map[string]any); ok {
		if w, ok := c["work_item"].(map[string]any); ok {
			context["work_item"] = w["id"]
		}
		if fields, ok := c["fields"].(map[string]any); ok && len(fields) > 0 {
			ids := map[string]any{}
			for key, v := range fields {
				if ref, ok := v.(map[string]any); ok {
					ids[key] = ref["id"]
				}
			}
			context["fields"] = ids
		}
	}
	b, e := json.Marshal([]any{doc, context})
	if e != nil {
		b = raw
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func scanDocumentReport(row documentScanner) (DocumentReport, error) {
	var v DocumentReport
	var summary string
	e := row.Scan(&v.ID, &v.ApplicationID, &v.Service, &v.Environment, &v.Protocol, &v.Actor, &v.Source, &v.State, &v.CreatedAt, &v.UpdatedAt, &v.ExpiresAt, &v.SnapshotID, &v.BaselineID, &summary, &v.Error, &v.RunID, &v.Commit, &v.RequestDigest, &v.Number)
	if e == nil {
		e = json.Unmarshal([]byte(summary), &v.Changes)
	}
	return v, e
}
func (s *Store) documentReport(id string) (DocumentReport, error) {
	return scanDocumentReport(s.db.QueryRow(`SELECT `+reportSelectColumns+` FROM api_document_reports WHERE id=?`, id))
}
func (s *Store) insertDocumentReport(v DocumentReport) error {
	b, _ := json.Marshal(v.Changes)
	_, e := s.db.Exec(`INSERT INTO api_document_reports (`+reportColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.ApplicationID, v.Service, v.Environment, v.Protocol, v.Actor, v.Source, v.State, v.CreatedAt, v.UpdatedAt, v.ExpiresAt, v.SnapshotID, v.BaselineID, string(b), v.Error, v.RunID, v.Commit, v.RequestDigest)
	return e
}
func (s *Store) publicDocumentReport(v DocumentReport) DocumentReport {
	links, e := scanReportLinks(s.db.QueryRow(reportLinksSelect, v.ID))
	if e != nil {
		v.AssociationError = "需求关联读取失败，请重试"
	} else {
		v.Association = &links
	}
	if v.State == "preview" && v.ExpiresAt <= time.Now().Unix() {
		v.State = "expired"
	}
	if v.State == "processing" && v.UpdatedAt < time.Now().Add(-2*time.Minute).Unix() {
		v.State = "uncertain"
	}
	_ = s.db.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, v.ApplicationID, v.Environment, v.Protocol).Scan(&v.CurrentID)
	return v
}

// The receipt is committed with the immutable snapshot, including no-op uploads.
func finishDocumentReport(tx *deployTx, id string, snapshot, baseline int64, state string, summary apidoc.ChangeSummary, context ...*DocumentChangeContext) error {
	if id == "" {
		return nil
	}
	b, _ := json.Marshal(summary)
	r, e := tx.Exec(`UPDATE api_document_reports SET state=?,snapshot_id=?,baseline_id=?,summary=?,updated_at=?,error='' WHERE id=? AND state='processing'`, state, snapshot, baseline, string(b), time.Now().Unix(), id)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		return fmt.Errorf("上报记录已变化，请核对原回执")
	}
	if e == nil && len(context) > 0 {
		return recordAutomaticReportLinks(tx, id, context[0])
	}
	return e
}

func finishDuplicateDocumentReport(tx *deployTx, sv *Service, snap APIDocumentSnapshot, raw []byte, protocol string, id int64) (int64, error) {
	var baseline int64
	var before string
	e := tx.QueryRow(`SELECT id,document FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, snap.Environment, protocol).Scan(&baseline, &before)
	if e != nil {
		return 0, e
	}
	diff, e := apidoc.Compare([]byte(before), raw)
	if e != nil {
		return 0, e
	}
	state := "unchanged"
	if id != baseline {
		state = "historical"
	}
	if e = finishDocumentReport(tx, snap.ReportID, id, baseline, state, diff.Summary, snap.ChangeContext); e != nil {
		return 0, e
	}
	if e = tx.Commit(); e != nil {
		return 0, errDocumentCommitUnknown
	}
	return id, nil
}

// Transport failures never trigger a second write. The same report identity
// returns its durable result, or remains blocked while its outcome is unknown.
func (s *Store) documentReportHTTP(next http.HandlerFunc, preview bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sv := s.GetService(q.Get("service"))
		env := q.Get("environment")
		if sv == nil || (env != "" && s.GetEnvironment(env) == nil) || (r.Header.Get("X-User") != "" && r.Header.Get("X-Role") != "admin") {
			next(w, r)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, apidoc.MaxDocument+65537))
		if e != nil || len(raw) > apidoc.MaxDocument+65536 {
			http.Error(w, `{"error":"文档过大或无法读取"}`, 400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		id := q.Get("report_id")
		if id == "" {
			id = newDocumentReportID()
		}
		if !reportIDPattern.MatchString(id) {
			http.Error(w, `{"error":"上报编号无效"}`, 400)
			return
		}
		protocol := q.Get("protocol")
		if protocol == "" {
			protocol = "http"
		}
		if protocol != "http" && protocol != "dubbo" {
			http.Error(w, `{"error":"文档协议无效"}`, 400)
			return
		}
		var envelope struct {
			Document json.RawMessage `json:"document"`
		}
		_ = json.Unmarshal(raw, &envelope)
		documentRaw := raw
		if len(envelope.Document) > 0 {
			documentRaw = envelope.Document
		}
		if d, _, _, err := apidoc.Parse(documentRaw); err == nil {
			protocol = d.Protocol()
		}
		if strings.Contains(r.URL.Path, "/edit") {
			if baseID, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
				if base, e := s.apiDocumentSnapshot(sv.ApplicationID, baseID); e == nil && base.Environment == env {
					protocol = base.Protocol
				}
			}
		}

		fingerprint := documentReportFingerprint(raw)
		actor := deliveryActor(r)
		source := "file"
		if documentKeyMaintainer(r) != "" {
			source = "mcp"
		}
		if strings.Contains(r.URL.Path, "/edit") {
			source = "edit"
		}
		now := time.Now().Unix()
		state := "processing"
		if preview {
			state = "preview"
		}
		v := DocumentReport{ID: id, ApplicationID: sv.ApplicationID, Service: sv.Name, Environment: env, Protocol: protocol, Actor: actor, Source: source, State: state, CreatedAt: now, UpdatedAt: now, ExpiresAt: now + 600, RequestDigest: fingerprint}
		old, err := s.documentReport(id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error":"上报记录不可用"}`, 500)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			if err = s.insertDocumentReport(v); err != nil {
				http.Error(w, `{"error":"上报编号已被占用，请查询原记录"}`, 409)
				return
			}
		} else {
			if old.Actor != actor || old.ApplicationID != sv.ApplicationID || old.Environment != env || old.Protocol != protocol || old.RequestDigest != fingerprint || old.Source != source {
				http.Error(w, `{"error":"上报身份或内容与原请求不一致"}`, 409)
				return
			}
			if old.State != "preview" || preview || old.ExpiresAt <= now {
				code := 409
				if old.SnapshotID > 0 {
					code = 200
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				result := map[string]any{"id": old.SnapshotID, "report": s.publicDocumentReport(old), "report_id": id, "protocol": old.Protocol, "environment": old.Environment, "source_id": old.BaselineID, "duplicate": old.State == "unchanged" || old.State == "historical", "unchanged": old.State == "unchanged"}
				if code != 200 {
					result["error"] = "请核对原上报记录，不会重复执行"
				}
				json.NewEncoder(w).Encode(result)
				return
			}
			if expected := q.Get("expected_baseline_id"); expected != strconv.FormatInt(old.BaselineID, 10) {
				http.Error(w, `{"error":"确认基线与原上报预览不一致"}`, 409)
				return
			}
			result, e := s.db.Exec(`UPDATE api_document_reports SET state='processing',updated_at=? WHERE id=? AND state='preview'`, now, id)
			if e != nil {
				http.Error(w, `{"error":"上报状态更新失败"}`, 500)
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				http.Error(w, `{"error":"上报已被处理"}`, 409)
				return
			}
		}
		q.Set("report_id", id)
		r.URL.RawQuery = q.Encode()
		recorder := httptest.NewRecorder()
		next(recorder, r)
		var body map[string]any
		decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
		decoder.UseNumber()
		_ = decoder.Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		if recorder.Code >= 400 {
			// Store only bounded platform messages, never parser output containing the document.
			status, message := "failed", "文档校验或保存未通过，请核对原请求"
			if recorder.Code >= 500 {
				status, message = "uncertain", "保存结果待核对，请查询原记录，不要重复提交"
			}
			if recorder.Code == 409 {
				message = "文档基线或上报状态已变化，请保留内容重新预览"
			}
			_, _ = s.db.Exec(`UPDATE api_document_reports SET state=?,error=?,updated_at=? WHERE id=? AND state IN ('processing','preview')`, status, message, time.Now().Unix(), id)
		} else if preview {
			baseline := int64(0)
			if n, ok := body["source_id"].(json.Number); ok {
				baseline, _ = n.Int64()
			}
			if b, ok := body["baseline"].(map[string]any); ok {
				if n, ok := b["id"].(json.Number); ok {
					baseline, _ = n.Int64()
				}
			}
			summary, _ := json.Marshal(body["summary"])
			if string(summary) == "null" {
				summary = []byte(`{}`)
			}
			if duplicate, _ := body["duplicate"].(bool); duplicate {
				fingerprint = documentReportFingerprint(documentRaw)
			}
			if candidate := body["candidate"]; candidate != nil {
				b, e := json.Marshal(map[string]any{"document": candidate, "change_context": body["change_context"]})
				if e == nil {
					fingerprint = documentReportFingerprint(b)
				}
			}
			_, err = s.db.Exec(`UPDATE api_document_reports SET baseline_id=?,summary=?,request_digest=? WHERE id=? AND state='preview'`, baseline, string(summary), fingerprint, id)
			if err != nil {
				http.Error(w, `{"error":"上报预览记录保存失败，请重新核对"}`, 500)
				return
			}
		}
		v, err = s.documentReport(id)
		if recorder.Code < 400 && preview {
			var submitted struct {
				Context *DocumentChangeContext `json:"change_context"`
			}
			if json.Unmarshal(raw, &submitted) == nil && submitted.Context != nil {
				// Preview may reuse an older snapshot's lineage in its response.
				// Link this attempt only to its own explicitly submitted context.
				candidateRaw := documentRaw
				if candidate := body["candidate"]; candidate != nil {
					candidateRaw, _ = json.Marshal(candidate)
				}
				resolved, e := s.resolveDocumentChanges(submitted.Context, candidateRaw)
				if e != nil {
					_, _ = s.db.Exec(`UPDATE api_document_reports SET state='failed',error='需求关联已变化，请重新预览',updated_at=? WHERE id=? AND state='preview'`, time.Now().Unix(), id)
					http.Error(w, `{"error":"需求关联已变化，请重新预览"}`, 409)
					return
				}
				tx, e := s.db.Begin()
				if e != nil {
					http.Error(w, `{"error":"关联记录保存失败"}`, 500)
					return
				}
				e = recordAutomaticReportLinks(tx, id, resolved)
				if e == nil {
					e = tx.Commit()
				} else {
					tx.Rollback()
				}
				if e != nil {
					_, _ = s.db.Exec(`UPDATE api_document_reports SET state='uncertain',error='关联记录保存结果待核对，文档尚未保存',updated_at=? WHERE id=? AND state='preview'`, time.Now().Unix(), id)
					http.Error(w, `{"error":"关联记录保存失败，请核对原上报记录"}`, 500)
					return
				}
			}
		}
		if err != nil {
			http.Error(w, `{"error":"回执读取失败，请按原上报编号核对"}`, 500)
			return
		}
		body["report_id"], body["report"] = id, s.publicDocumentReport(v)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(recorder.Code)
		json.NewEncoder(w).Encode(body)
	}
}

func registerDocumentReportRoutes(mux *http.ServeMux, s *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any)) {
	registerDocumentReportLinkRoutes(mux, s, wj, fail)
	mux.HandleFunc("GET /api/api-document-reports", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		if page > 100000 {
			fail(w, 400, "页码过大")
			return
		}
		where := " WHERE 1=1"
		args := []any{}
		for _, key := range []string{"service", "environment", "protocol", "actor", "source", "state"} {
			if q.Has(key) && q.Get(key) != "" {
				if len(q.Get(key)) > 191 {
					fail(w, 400, "筛选值过长")
					return
				}
				column := key
				if key == "state" {
					column = fmt.Sprintf("(CASE WHEN state='preview' AND expires_at<=%d THEN 'expired' WHEN state='processing' AND updated_at<%d THEN 'uncertain' ELSE state END)", time.Now().Unix(), time.Now().Add(-2*time.Minute).Unix())
				}
				where += " AND " + column + "=?"
				args = append(args, q.Get(key))
			}
		}
		if q.Get("mine") == "1" {
			where += " AND actor=?"
			args = append(args, deliveryActor(r))
		}
		for _, key := range []string{"start", "end"} {
			if q.Get(key) != "" {
				v, e := strconv.ParseInt(q.Get(key), 10, 64)
				if e != nil || v < 0 {
					fail(w, 400, "时间范围无效")
					return
				}
				op := ">="
				if key == "end" {
					op = "<="
				}
				where += " AND created_at" + op + "?"
				args = append(args, v)
			}
		}
		var total int
		if e := s.db.QueryRow(`SELECT COUNT(*) FROM api_document_reports`+where, args...).Scan(&total); e != nil {
			fail(w, 500, "上报记录读取失败")
			return
		}
		rows, e := s.db.Query(`SELECT `+reportSelectColumns+` FROM api_document_reports`+where+` ORDER BY created_at DESC,sequence DESC LIMIT 20 OFFSET ?`, append(args, (page-1)*20)...)
		if e != nil {
			fail(w, 500, "上报记录读取失败")
			return
		}
		items := []DocumentReport{}
		for rows.Next() {
			v, e := scanDocumentReport(rows)
			if e != nil {
				rows.Close()
				fail(w, 500, "上报记录读取失败")
				return
			}
			items = append(items, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			fail(w, 500, e)
			return
		}
		for i := range items {
			items[i] = s.publicDocumentReport(items[i])
			items[i].ActorName = personnames.FromRequest(r).Name(items[i].Actor)
		}
		wj(w, map[string]any{"items": items, "total": total, "page": page, "page_size": 20})
	})
	mux.HandleFunc("GET /api/api-document-reports/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.documentReport(r.PathValue("id"))
		if e != nil {
			fail(w, 404, "上报记录不存在")
			return
		}
		for _, key := range []string{"service", "environment", "protocol"} {
			actual := map[string]string{"service": v.Service, "environment": v.Environment, "protocol": v.Protocol}[key]
			if r.URL.Query().Has(key) && r.URL.Query().Get(key) != actual {
				fail(w, 404, "上报记录不属于指定范围")
				return
			}
		}
		v = s.publicDocumentReport(v)
		v.ActorName = personnames.FromRequest(r).Name(v.Actor)
		result := map[string]any{"report": v}
		if v.SnapshotID > 0 {
			snap, e := s.apiDocumentSnapshot(v.ApplicationID, v.SnapshotID)
			if e != nil || snap.Environment != v.Environment || snap.Protocol != v.Protocol {
				fail(w, 409, "上报文档归属无法核对")
				return
			}
			var before []byte
			if v.BaselineID > 0 {
				base, e := s.apiDocumentSnapshot(v.ApplicationID, v.BaselineID)
				if e != nil || base.Environment != v.Environment || base.Protocol != v.Protocol {
					fail(w, 409, "上报基线无法核对")
					return
				}
				before = base.Document
			}
			diff, e := apidoc.Compare(before, snap.Document)
			if e != nil {
				fail(w, 500, "变化分析失败")
				return
			}
			result["comparison"] = diff
		}
		wj(w, result)
	})
	mux.HandleFunc("POST /api/api-document-reports/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.documentReport(r.PathValue("id"))
		if e != nil || v.Actor != deliveryActor(r) {
			fail(w, 403, "仅本人可取消自己的上报预览")
			return
		}
		result, e := s.db.Exec(`UPDATE api_document_reports SET state='cancelled',updated_at=? WHERE id=? AND state='preview' AND expires_at>?`, time.Now().Unix(), v.ID, time.Now().Unix())
		if e != nil {
			fail(w, 500, e)
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			fail(w, 409, "上报已处理或预览已过期")
			return
		}
		v, _ = s.documentReport(v.ID)
		wj(w, s.publicDocumentReport(v))
	})
}
