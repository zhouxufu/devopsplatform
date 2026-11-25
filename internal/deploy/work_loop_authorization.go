package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type workLoopAuthorizationRequest struct {
	Revision       int64        `json:"revision"`
	Spec           WorkLoopSpec `json:"spec"`
	ExpectedDigest string       `json:"expected_digest,omitempty"`
}
type workLoopGrantDifference struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}
type workLoopAuthorizationPreview struct {
	Authority         string                    `json:"authority,omitempty"`
	KeyID             string                    `json:"authority_key_id,omitempty"`
	PreviousAuthority string                    `json:"previous_authority,omitempty"`
	PreviousKeyID     string                    `json:"previous_authority_key_id,omitempty"`
	Revision          int64                     `json:"revision"`
	PreviousDigest    string                    `json:"previous_digest"`
	Grant             *WorkLoopGrant            `json:"grant"`
	Differences       []workLoopGrantDifference `json:"differences"`
	NewIteration      bool                      `json:"new_iteration"`
	Digest            string                    `json:"digest"`
}

func workLoopGrantTerms(g WorkLoopGrant) WorkLoopGrant {
	g.Digest = ""
	g.Spec.ExpiresAt = 0
	g.Spec.MaxIterations = 0
	return g
}
func (s *Store) previewWorkLoopAuthorization(ctx context.Context, t *WorkLoop, req workLoopAuthorizationRequest) (*workLoopAuthorizationPreview, error) {
	if req.Revision != t.Revision {
		return nil, ErrRunConflict
	}
	if t.Revoked {
		return nil, fmt.Errorf("授权已撤销，请新建跟进任务；原执行记录保留")
	}
	if req.Spec.DraftOnly && !t.Grant.Spec.DraftOnly {
		return nil, fmt.Errorf("请为不同用途单独授权")
	}
	if req.Spec.WorkItemID != t.Grant.Spec.WorkItemID {
		return nil, fmt.Errorf("不能将任务授权转移到另一个工作项")
	}
	if t.Intent != nil && t.Intent.State == "dispatching" {
		return nil, fmt.Errorf("原操作结果待核对，续授权不能解除执行保护")
	}
	g, e := s.previewWorkLoop(ctx, req.Spec, t.Owner)
	if e != nil {
		return nil, e
	}
	changed := workLoopValueDigest(workLoopGrantTerms(t.Grant)) != workLoopValueDigest(workLoopGrantTerms(*g))
	unfrozen := t.Stage == "development" && t.current().PreparationID == 0 && t.current().DeliveryID == 0 && len(t.current().ChangeIDs) == 0
	next := changed && !unfrozen
	if next && !s.workLoopMayIterate(t) {
		return nil, fmt.Errorf("范围发生变化，原轮次尚未安全结束；请在当前执行单核对并取消或完成后再续授权，不能替换已冻结范围")
	}
	p := &workLoopAuthorizationPreview{Revision: t.Revision, PreviousDigest: t.Grant.Digest, Grant: g, NewIteration: next, Differences: []workLoopGrantDifference{}}
	// Field values contain policy and identity metadata only, never credentials.
	var before, after map[string]any
	b, _ := json.Marshal(t.Grant)
	_ = json.Unmarshal(b, &before)
	b, _ = json.Marshal(g)
	_ = json.Unmarshal(b, &after)
	for _, k := range []string{"spec", "template_id", "workflow_digest", "policies", "regression", "source"} {
		if workLoopValueDigest(before[k]) != workLoopValueDigest(after[k]) {
			p.Differences = append(p.Differences, workLoopGrantDifference{k, before[k], after[k]})
		}
	}
	p.Digest = digestJSON(p)
	return p, nil
}
func (s *Store) applyWorkLoopAuthorization(t *WorkLoop, p *workLoopAuthorizationPreview) error {
	if t.Revision != p.Revision || t.Grant.Digest != p.PreviousDigest {
		return ErrRunConflict
	}
	if len(t.Authorizations) == 0 {
		t.Authorizations = append(t.Authorizations, t.Grant)
	}
	if t.current().GrantDigest == "" {
		t.current().GrantDigest = t.Grant.Digest
	}
	if p.NewIteration {
		t.current().State = t.State
		t.Iterations = append(t.Iterations, WorkLoopIteration{Number: len(t.Iterations) + 1, ChangeIDs: []int64{}, State: "developing", GrantDigest: p.Grant.Digest})
		t.Intent = nil
		t.setState("developing", "development", "已确认新范围，开始新的开发轮次；原执行及回归快照保留")
	} else if t.Stage == "development" {
		t.current().GrantDigest = p.Grant.Digest
	}
	t.Grant = *p.Grant
	t.Authorizations = append(t.Authorizations, *p.Grant)
	t.Events = append(t.Events, WorkLoopEvent{At: time.Now().Unix(), State: t.State, Message: "本人已核对并续授权；未派发业务操作"})
	return s.saveWorkLoop(t)
}
func registerWorkLoopAuthorizationRoutes(mux *http.ServeMux, s *Store) {
	for _, preview := range []bool{true, false} {
		path := "POST /api/work-loops/{id}/authorization"
		if preview {
			path += "/preview"
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			t, e := s.workLoop(r.PathValue("id"))
			if e != nil {
				deliveryWriteError(w, 404, e)
				return
			}
			if t.Owner != deliveryActor(r) {
				deliveryWriteError(w, 403, "仅任务授权人可续授权")
				return
			}
			var req workLoopAuthorizationRequest
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
			dec.DisallowUnknownFields()
			if dec.Decode(&req) != nil {
				deliveryWriteError(w, 400, "续授权请求无效")
				return
			}
			p, e := s.previewWorkLoopAuthorization(r.Context(), t, req)
			if e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			p.Authority, p.KeyID = r.Header.Get("X-Work-Loop-Authority"), r.Header.Get("X-Work-Loop-Key-ID")
			p.PreviousAuthority, p.PreviousKeyID = t.Authority, t.AuthorityKeyID
			p.Digest = ""
			p.Digest = digestJSON(p)
			if preview {
				deliveryJSON(w, p)
				return
			}
			if req.ExpectedDigest != p.Digest {
				deliveryWriteError(w, 409, "续授权预览已变化，请重新核对差异")
				return
			}
			t.Authority = r.Header.Get("X-Work-Loop-Authority")
			t.AuthorityKeyID = r.Header.Get("X-Work-Loop-Key-ID")
			if e = s.applyWorkLoopAuthorization(t, p); e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			s.Audit(t.Owner, "reauthorize-work-loop", "work-loop", t.ID, p)
			deliveryJSON(w, t)
		})
	}
}

// Internal metadata-only status update. Never clears an intent or execution lock.
func registerWorkLoopBackgroundStatus(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /internal/work-loops/{id}/blocked", func(w http.ResponseWriter, r *http.Request) {
		t, e := s.workLoop(r.PathValue("id"))
		if e != nil || t.Owner != deliveryActor(r) {
			deliveryWriteError(w, 403, "任务身份无效")
			return
		}
		var req struct {
			Revision int64  `json:"revision"`
			Reason   string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil || req.Revision != t.Revision || len(req.Reason) > 4000 {
			deliveryWriteError(w, 409, "任务状态已变化")
			return
		}
		if t.State == "running" {
			t.setState("blocked", t.Stage, req.Reason)
			if e = s.saveWorkLoop(t); e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
		}
		deliveryJSON(w, t)
	})
}

// Persisted interface fields decode as maps; freshly built policies contain
// typed structs. Compare normalized JSON values, not their property order.
func workLoopValueDigest(value any) string {
	raw, _ := json.Marshal(value)
	var normalized any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&normalized)
	return digestJSON(normalized)
}
