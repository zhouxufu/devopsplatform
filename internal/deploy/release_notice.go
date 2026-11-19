package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ReleaseNoticeEntry struct {
	Source     string `json:"source"`
	BugID      string `json:"bug_id"`
	Title      string `json:"title"`
	FeedbackBy string `json:"feedback_by"`
}
type ReleaseNotice struct {
	Text        string               `json:"text"`
	Revision    int64                `json:"revision"`
	ScopeDigest string               `json:"scope_digest"`
	Entries     []ReleaseNoticeEntry `json:"entries"`
	Warnings    []string             `json:"warnings"`
	UpdatedAt   int64                `json:"updated_at"`
	Stale       bool                 `json:"stale"`
}
type noticeTarget struct {
	title, environment, status, owner, digest string
	items                                     []WorkItem
}

func noticeScopeItems(scope *DeliveryScope) []WorkItem {
	if scope == nil {
		return nil
	}
	linked := map[int64]WorkItem{}
	for _, c := range scope.Changes {
		for _, w := range c.WorkItems {
			linked[w.ID] = w
		}
	}
	out := []WorkItem{}
	for _, w := range scope.WorkItems {
		if len(scope.ExcludedChangeIDs) == 0 || len(linked) == 0 {
			out = append(out, w)
		} else if _, ok := linked[w.ID]; ok {
			out = append(out, w)
		}
	}
	// Independent change delivery also carries the selected changes' Bug links.
	if len(scope.WorkItems) == 0 {
		for _, c := range scope.Changes {
			out = append(out, c.WorkItems...)
		}
	}
	return out
}

func (s *Store) releaseNoticeTarget(kind string, id int64) (*noticeTarget, error) {
	t := &noticeTarget{}
	switch kind {
	case "delivery":
		run := s.DeliveryRun(id)
		if run == nil {
			return nil, fmt.Errorf("交付记录不存在")
		}
		t.title, t.environment, t.status, t.owner = run.Plan.Template.Name, run.Plan.Environment.DisplayName, run.Status, run.Owner
		if run.Plan.Version != nil {
			t.title = run.Plan.Version.Version + " · " + run.Plan.Version.Title
		}
		if t.environment == "" {
			t.environment = run.Plan.Environment.Name
		}
		if run.Plan.Scope != nil {
			t.items = noticeScopeItems(run.Plan.Scope)
		} else if run.Plan.Version != nil {
			t.items = run.Plan.Version.WorkItems
		}
		t.digest = digestJSON([]any{kind, id, run.Digest, run.Status})
	case "version":
		v := s.GetReleaseVersion(id)
		if v == nil {
			return nil, fmt.Errorf("发布版本不存在")
		}
		t.title, t.status, t.owner, t.items = v.Version+" · "+v.Title, v.Status, v.Owner, v.WorkItems
		t.digest = digestJSON([]any{kind, id, v.Version, v.Title, v.Status, v.WorkItems, v.Changes})
	case "release":
		r := s.GetReleaseOrder(id)
		if r == nil {
			return nil, fmt.Errorf("发布记录不存在")
		}
		// Child releases use their immutable parent scope, not the application's
		// current list of Bugs (which could include unrelated future changes).
		rows, err := s.db.Query(`SELECT id,execution FROM delivery_runs ORDER BY id DESC`)
		if err != nil {
			return nil, err
		}
		var parent int64
		for rows.Next() {
			var rid int64
			var raw string
			if err = rows.Scan(&rid, &raw); err != nil {
				break
			}
			var steps []DeliveryStep
			if json.Unmarshal([]byte(raw), &steps) != nil {
				continue
			}
			for _, step := range steps {
				if step.ReleaseID == id {
					parent = rid
					break
				}
			}
			if parent > 0 {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		if parent > 0 {
			t, err = s.releaseNoticeTarget("delivery", parent)
			if err != nil {
				return nil, err
			}
		}
		t.title, t.environment, t.status, t.owner = r.Service+" · "+r.Version, r.Environment, r.Status, r.Actor
		t.digest = digestJSON([]any{kind, id, t.digest, r.Service, r.Version, r.Environment, r.Status})
	default:
		return nil, fmt.Errorf("无效发布记录类型")
	}
	return t, nil
}

func noticeLine(s string) string { return strings.Join(strings.Fields(s), " ") }
func buildReleaseNotice(t *noticeTarget, entries []ReleaseNoticeEntry, warnings []string) *ReleaseNotice {
	status := map[string]string{"success": "发布成功", "failed": "发布失败", "running": "发布中", "approved": "待执行", "pending_approval": "待审批", "ready": "已就绪", "draft": "草稿", "completed": "已完成", "canceled": "已取消", "rolled_back": "已回退"}[t.status]
	if status == "" {
		status = t.status
	}
	text := "【发布说明】" + noticeLine(t.title)
	if t.environment != "" {
		text += "\n环境：" + noticeLine(t.environment)
	}
	text += "\n状态：" + noticeLine(status) + "\n\n本次 Bug："
	for i, e := range entries {
		who := noticeLine(e.FeedbackBy)
		if who == "" {
			who = "（未填写反馈者）"
		} else {
			names := strings.FieldsFunc(who, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' || r == '\n' })
			for i, name := range names {
				names[i] = "@" + strings.TrimPrefix(strings.TrimSpace(name), "@")
			}
			who = strings.Join(names, " ")
		}
		text += fmt.Sprintf("\n%d. #%s %s %s", i+1, e.BugID, noticeLine(e.Title), who)
	}
	if len(entries) == 0 {
		text += "\n本次未关联 Bug，请补充发布内容。"
	}
	return &ReleaseNotice{Text: text, ScopeDigest: t.digest, Entries: entries, Warnings: warnings}
}

func (s *Store) generateReleaseNotice(ctx context.Context, t *noticeTarget, actor string) *ReleaseNotice {
	entries := []ReleaseNoticeEntry{}
	warnings := []string{}
	seen := map[string]bool{}
	clients := map[string]*zentaoLegacy{}
	connections := map[string]*ResourceConnection{}
	failed := map[string]bool{}
	for _, w := range t.items {
		if w.Kind != "bug" {
			continue
		}
		key := w.Source + ":" + w.ExternalID
		if w.ExternalID == "" {
			key = fmt.Sprint(w.ID)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		id := w.ExternalID
		if id == "" {
			id = fmt.Sprint(w.ID)
		}
		e := ReleaseNoticeEntry{Source: w.Source, BugID: id, Title: w.Title}
		if strings.HasPrefix(w.Source, "zentao:") {
			name := strings.TrimPrefix(w.Source, "zentao:")
			if len(entries) >= 100 {
				warnings = append(warnings, "超过 100 条 Bug，剩余反馈者请人工补充")
			} else {
				if clients[name] == nil && !failed[name] {
					z, c, _, err := s.liveReadClient(ctx, name, actor, false)
					if err != nil {
						failed[name] = true
					} else {
						clients[name], connections[name] = z, c
					}
				}
				if z := clients[name]; z != nil {
					remote, err := z.researchRead(ctx, w.ExternalID, "view")
					if err == nil {
						product, _ := strconv.Atoi(researchValue(remote.Bug, "product"))
						if product != 0 && !liveProductAllowed(connections[name], product) {
							err = fmt.Errorf("来源范围已变化")
						}
					}
					if err == nil {
						e.FeedbackBy = researchValue(remote.Bug, "feedbackBy")
					} else {
						warnings = append(warnings, "Bug #"+id+" 的反馈者读取失败，请核对来源权限")
					}
				} else {
					warnings = append(warnings, "Bug #"+id+" 的来源连接或授权不可用，请手工补充反馈者")
				}
			}
		}
		if strings.TrimSpace(e.FeedbackBy) == "" {
			warnings = append(warnings, "Bug #"+id+" 尚无可用反馈者，未使用创建人或解决人替代")
		}
		entries = append(entries, e)
	}
	return buildReleaseNotice(t, entries, warnings)
}

func (s *Store) savedReleaseNotice(kind string, id int64) (*ReleaseNotice, error) {
	var raw string
	var rev, at int64
	err := s.db.QueryRow(`SELECT document,revision,updated_at FROM release_notices WHERE record_kind=? AND record_id=?`, kind, id).Scan(&raw, &rev, &at)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var n ReleaseNotice
	if err = json.Unmarshal([]byte(raw), &n); err != nil {
		return nil, err
	}
	n.Revision, n.UpdatedAt = rev, at
	return &n, nil
}
func (s *Store) saveReleaseNotice(kind string, id int64, n *ReleaseNotice, actor string) error {
	if len(n.Text) > 65536 || strings.TrimSpace(n.Text) == "" {
		return fmt.Errorf("发布说明不能为空且不能超过 64 KiB")
	}
	t, err := s.releaseNoticeTarget(kind, id)
	if err != nil {
		return err
	}
	if n.ScopeDigest != t.digest {
		return fmt.Errorf("发布范围已变化，请重新生成并核对说明")
	}
	raw, _ := json.Marshal(n)
	now := time.Now().Unix()
	if n.Revision == 0 {
		_, err = s.db.Exec(`INSERT INTO release_notices(record_kind,record_id,document,revision,actor,updated_at) VALUES(?,?,?,1,?,?)`, kind, id, string(raw), actor, now)
		if err != nil {
			return fmt.Errorf("发布说明已被保存，请重新读取后合并修改")
		}
	} else {
		r, e := s.db.Exec(`UPDATE release_notices SET document=?,revision=revision+1,actor=?,updated_at=? WHERE record_kind=? AND record_id=? AND revision=?`, string(raw), actor, now, kind, id, n.Revision)
		if e != nil {
			return e
		}
		count, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if count != 1 {
			return fmt.Errorf("发布说明已被其他人修改，请重新读取后合并")
		}
	}
	s.Audit(actor, "save-release-notice", kind, fmt.Sprint(id), map[string]any{"revision": n.Revision + 1})
	return nil
}
func registerReleaseNoticeRoutes(mux *http.ServeMux, s *Store) {
	for _, method := range []string{"GET", "POST", "PUT"} {
		mux.HandleFunc(method+" /api/release-notices/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
			kind := r.PathValue("kind")
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil || id <= 0 {
				deliveryWriteError(w, 400, "记录编号无效")
				return
			}
			t, err := s.releaseNoticeTarget(kind, id)
			if err != nil {
				deliveryWriteError(w, 404, err)
				return
			}
			actor := deliveryActor(r)
			n, err := s.savedReleaseNotice(kind, id)
			if err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			if r.Method == "GET" {
				if n == nil {
					n = &ReleaseNotice{ScopeDigest: t.digest, Entries: []ReleaseNoticeEntry{}, Warnings: []string{}}
				}
				n.Stale = n.ScopeDigest != t.digest
				deliveryJSON(w, n)
				return
			}
			if r.Method == "POST" {
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				defer cancel()
				fresh := s.generateReleaseNotice(ctx, t, actor)
				if n != nil {
					fresh.Revision = n.Revision
				}
				deliveryJSON(w, fresh)
				return
			}
			if !deliveryManage(r, t.owner) {
				deliveryWriteError(w, 403, "仅记录维护人或管理员可保存发布说明")
				return
			}
			var input ReleaseNotice
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&input) != nil {
				deliveryWriteError(w, 400, "发布说明格式无效")
				return
			}
			if err = s.saveReleaseNotice(kind, id, &input, actor); err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			out, err := s.savedReleaseNotice(kind, id)
			if err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			deliveryJSON(w, out)
		})
	}
}
