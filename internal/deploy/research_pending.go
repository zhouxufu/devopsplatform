package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const pendingBatchSize = 100
const pendingCandidateLimit = 5000
const bugListFields = "id,product,execution,project,title,status,severity,pri,resolution,assignedTo,openedBy,resolvedBy,closedBy,lastEditedDate,deleted"

type zentaoModelDisabledError struct{}

func (*zentaoModelDisabledError) Error() string { return "来源未启用 apiGetModel" }

// Only this fixed, read-only model is exposed internally. Never accept model,
// method, fields, SQL, or an empty ID list from a caller (empty means all Bugs).
func validateBugListModelArgs(args url.Values) error {
	if len(args) != 3 || args.Get("moduleName") != "bug" || args.Get("methodName") != "getByList" {
		return fmt.Errorf("只允许按编号批量读取 Bug")
	}
	for _, values := range args {
		if len(values) != 1 {
			return fmt.Errorf("批量读取参数无效")
		}
	}
	params := args.Get("params")
	if !strings.HasPrefix(params, "bugIDList=") || !strings.HasSuffix(params, ",fields="+bugListFields) {
		return fmt.Errorf("批量读取参数无效")
	}
	ids := strings.Split(strings.TrimSuffix(strings.TrimPrefix(params, "bugIDList="), ",fields="+bugListFields), ",")
	if len(ids) > pendingBatchSize {
		return fmt.Errorf("批量读取范围过大")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id || seen[id] {
			return fmt.Errorf("批量 Bug 编号无效")
		}
		seen[id] = true
	}
	return nil
}

// GET user/login with an already authenticated cookie returns the current
// source session's user, rights and granted view. It does not send credentials
// or perform a login retry. Scope is refreshed on every nonempty query, not
// inferred from the platform actor, product allowlist, or stored Bug snapshot.
type pendingSourceUser struct {
	User struct {
		Account  string `json:"account"`
		Realname string `json:"realname"`
		Admin    *bool  `json:"admin"`
		View     *struct {
			Products *string `json:"products"`
			Sprints  *string `json:"sprints"`
		} `json:"view"`
		Rights struct {
			Rights map[string]map[string]json.RawMessage `json:"rights"`
			ACLs   struct {
				Views json.RawMessage `json:"views"`
			} `json:"acls"`
		} `json:"rights"`
	} `json:"user"`
}

func sourceIDSet(raw *string) (map[int]bool, error) {
	if raw == nil {
		return nil, fmt.Errorf("来源未返回完整可见范围")
	}
	out := map[int]bool{}
	for _, part := range strings.Split(*raw, ",") {
		if part == "" {
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("来源可见范围不兼容")
		}
		out[id] = true
	}
	return out, nil
}

func (s *Store) livePendingRelease(ctx context.Context, z *zentaoLegacy, c *ResourceConnection, f researchLiveFilter, out researchLivePage, baseDigest string) (researchLivePage, bool, error) {
	// Only platform-owned intent/evidence is filtered locally. Source status,
	// resolver, title and severity may have changed and are never filtered using
	// stale local copies. Large queues retain native cursor scanning as fallback.
	ids := []string{}
	rules, err := s.researchReleaseRules(c.Name)
	if err != nil {
		return out, true, err
	}
	productionOnly := f.Delivery == "pending_release"
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return out, true, err
		}
		intent, delivery := "", ""
		if productionOnly {
			delivery = "not_recorded"
			if !rules.Enabled {
				intent = "yes"
			}
		}
		candidates, err := s.WorkCandidates(WorkCandidateFilter{IDs: f.RegisteredIDs, Source: "zentao:" + c.Name, Delivery: delivery, ProductionOnly: productionOnly, ExcludeManualRelease: productionOnly, WorkItemFilter: WorkItemFilter{Kind: "bug", ReleaseRequired: intent, Relation: f.Relation, RelationActor: f.RelationActor, Page: page, PageSize: 100}})
		if err != nil {
			return out, true, err
		}
		if candidates.Total > pendingCandidateLimit {
			return out, false, nil
		}
		for _, item := range candidates.Items {
			id, err := strconv.ParseInt(item.ExternalID, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != item.ExternalID {
				return out, true, fmt.Errorf("来源交付关联编号异常")
			}
			ids = append(ids, item.ExternalID)
		}
		if page*100 >= candidates.Total {
			break
		}
	}
	digest := digestJSON([]any{baseDigest, "pending-batch-v1", ids})
	if f.Page > 1 || f.Cursor != "" {
		var cursor liveCursor
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || len(raw) > 512 || json.Unmarshal(raw, &cursor) != nil || cursor.Digest != digest || cursor.Page != f.Page || cursor.Index != 0 {
			return out, true, fmt.Errorf("待上线范围或查询游标已变化，请重新查询第一页")
		}
	}
	out.TotalExact, out.QueryMode = true, "candidate_batch"
	if len(ids) == 0 {
		// No source search, model call or result cache is needed to prove that
		// no explicit platform release intent is pending.
		return out, true, nil
	}
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	var user pendingSourceUser
	if err := z.call(ctx, "user", "login", nil, nil, &user); err != nil {
		return out, true, err
	}
	if user.User.Account != c.Username {
		return out, true, z.expireAuthentication()
	}
	if user.User.Realname != "" {
		out.People[user.User.Account] = user.User.Realname
	}
	if user.User.Admin == nil || user.User.View == nil {
		return out, false, nil // Older/custom source: use native permission filtering.
	}
	products, err := sourceIDSet(user.User.View.Products)
	if err != nil {
		return out, true, err
	}
	sprints, err := sourceIDSet(user.User.View.Sprints)
	if err != nil {
		return out, true, err
	}
	if !*user.User.Admin {
		if _, ok := user.User.Rights.Rights["bug"]["browse"]; !ok {
			return out, true, fmt.Errorf("来源账号无 Bug 列表查询权限")
		}
		views := map[string]json.RawMessage{}
		rawViews := string(user.User.Rights.ACLs.Views)
		if rawViews != "" && rawViews != "[]" && rawViews != "null" {
			if json.Unmarshal(user.User.Rights.ACLs.Views, &views) != nil {
				return out, true, fmt.Errorf("来源视图权限不兼容")
			}
		}
		if len(views) > 0 {
			if _, ok := views["qa"]; !ok {
				return out, true, fmt.Errorf("来源账号无测试视图查询权限")
			}
		}
	}
	predicates, err := livePredicates(f.WorkItemFilter)
	if err != nil {
		return out, true, err
	}
	matched := []map[string]any{}
	for start := 0; start < len(ids); start += pendingBatchSize {
		batch := ids[start:min(start+pendingBatchSize, len(ids))]
		args := url.Values{"moduleName": {"bug"}, "methodName": {"getByList"}, "params": {"bugIDList=" + strings.Join(batch, ",") + ",fields=" + bugListFields}}
		var raw json.RawMessage
		if err = z.call(ctx, "api", "getModel", args, nil, &raw); err != nil {
			var disabled *zentaoModelDisabledError
			if errors.As(err, &disabled) {
				return out, false, nil // Native list remains available; never fall back to cached Bugs.
			}
			return out, true, err
		}
		rows, err := decodeLiveBugs(raw)
		if err != nil || len(rows) > len(batch) {
			return out, true, fmt.Errorf("来源批量 Bug 响应不兼容")
		}
		requested, seen := map[string]bool{}, map[string]bool{}
		for _, id := range batch {
			requested[id] = true
		}
		for _, row := range rows {
			id := researchValue(row, "id")
			product, pe := strconv.Atoi(researchValue(row, "product"))
			execution, ee := strconv.Atoi(researchValue(row, "execution"))
			if !requested[id] || seen[id] || pe != nil || ee != nil || product <= 0 || execution < 0 || researchValue(row, "title") == "" {
				return out, true, fmt.Errorf("来源返回无效或越界的批量 Bug")
			}
			seen[id] = true
			if !liveProductAllowed(c, product) || !products[product] || (f.Product > 0 && product != f.Product) || (!*user.User.Admin && execution != 0 && !sprints[execution]) || researchValue(row, "deleted") != "0" {
				continue
			}
			matches := true
			for _, p := range predicates {
				value := researchValue(row, p.Field)
				if p.Operator == "=" && value != p.Value || p.Operator == "!=" && value == p.Value || p.Operator == ">=" && value < p.Value || p.Operator == "<=" && value > p.Value || p.Operator == "include" && !strings.Contains(strings.ToLower(value), strings.ToLower(p.Value)) {
					matches = false
				}
			}
			if matches {
				item, e := s.projectLiveBug(c, z, row, out.CheckedAt)
				if e != nil {
					return out, true, e
				}
				if liveIntentMatches(f.ReleaseRequired, item.ReleaseRequired) {
					matched = append(matched, row)
				}
			}
		}
	}
	sortLiveBugs(matched, liveOrder(f.WorkItemFilter))
	out.Total = len(matched)
	out.Page = min(f.Page, max(1, (out.Total+f.PageSize-1)/f.PageSize))
	start := min((out.Page-1)*f.PageSize, len(matched))
	selected := matched[start:min(start+f.PageSize, len(matched))]
	selectedIDs := []string{}
	for _, row := range selected {
		selectedIDs = append(selectedIDs, researchValue(row, "id"))
	}
	references, err := s.liveLocalReferences(ctx, c.Name, selectedIDs)
	if err != nil {
		return out, true, err
	}
	for _, row := range selected {
		id := researchValue(row, "id")
		if references[id] == nil {
			return out, true, fmt.Errorf("待上线关联或上线意图已变化，请重新查询")
		}
		item, err := s.projectLiveBug(c, z, row, out.CheckedAt, references[id])
		if err != nil {
			return out, true, err
		}
		item.ProductID, _ = strconv.Atoi(researchValue(row, "product"))
		out.Items = append(out.Items, item)
	}
	out.HasMore = out.Page*f.PageSize < out.Total
	if out.HasMore {
		raw, _ := json.Marshal(liveCursor{Page: out.Page + 1, Digest: digest})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, true, nil
}
