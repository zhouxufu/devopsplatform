package deploy

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Store) projectLiveRequirement(c *ResourceConnection, z *zentaoLegacy, row map[string]any) (researchLiveItem, error) {
	id := researchValue(row, "id")
	if n, e := strconv.ParseInt(id, 10, 64); e != nil || n <= 0 {
		return researchLiveItem{}, fmt.Errorf("来源需求编号无效")
	}
	var localID int64
	e := s.db.QueryRow(`SELECT work_item_id FROM external_work_items WHERE connection=? AND kind='requirement' AND external_id=?`, c.Name, id).Scan(&localID)
	if e != nil && e != sql.ErrNoRows {
		return researchLiveItem{}, e
	}
	var local *WorkItem
	if localID > 0 {
		local = s.GetWorkItem(localID)
		if local == nil || local.Kind != "requirement" || local.Source != "zentao:"+c.Name || local.ExternalID != id {
			return researchLiveItem{}, fmt.Errorf("需求来源关联异常")
		}
	}
	// Share factual columns and delivery rules, with an explicitly typed reference.
	item, e := s.projectLiveBug(c, z, row, time.Now().Unix(), local)
	if e != nil {
		return item, e
	}
	item.Kind = "requirement"
	item.Description = researchValue(row, "spec")
	item.DescriptionFormat = "html"
	if z.pathInfo {
		item.URL = z.root + "/story-view-" + url.PathEscape(id) + ".html"
	} else {
		item.URL = z.root + "/index.php?m=story&f=view&storyID=" + url.QueryEscape(id)
	}
	return item, nil
}
func (s *Store) AttachResearchRequirement(ctx context.Context, actor, name, id string) (*WorkItem, error) {
	if n, e := strconv.ParseInt(id, 10, 64); e != nil || n <= 0 {
		return nil, fmt.Errorf("来源需求编号无效")
	}
	z, c, _, e := s.liveReadClient(ctx, name, actor, false)
	if e != nil {
		return nil, e
	}
	remote, e := z.researchReadItem(ctx, &WorkItem{Kind: "requirement", ExternalID: id}, "view")
	if e != nil {
		return nil, e
	}
	product, _ := strconv.Atoi(researchValue(remote.Story, "product"))
	if !liveProductAllowed(c, product) {
		return nil, fmt.Errorf("需求不在允许产品范围")
	}
	item, e := s.projectLiveRequirement(c, z, remote.Story)
	if e != nil {
		return nil, e
	}
	if item.ID > 0 {
		return s.GetWorkItem(item.ID), nil
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	result, e := tx.Exec(`INSERT INTO work_items(source,external_id,kind,title,url,external_status,description,status,owner,created_at,updated_at,assigned_to,opened_by,resolved_by,closed_by,description_format) VALUES(?,?,'requirement',?,?,?,?,?,?,?,?,?,?,?,?,'html')`, item.Source, id, item.Title, item.URL, item.ExternalStatus, item.Description, item.Status, actor, now, now, item.AssignedTo, item.OpenedBy, item.ResolvedBy, item.ClosedBy)
	if e != nil {
		return nil, e
	}
	localID, e := result.LastInsertId()
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(`INSERT INTO external_work_items(connection,kind,external_id,work_item_id) VALUES(?,'requirement',?,?)`, name, id, localID); e != nil {
		return nil, fmt.Errorf("需求已被另一请求关联，请重新读取；未重复新增")
	}
	if e = saveIssueFields(tx, localID, item.ResearchIssueFields); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.GetWorkItem(localID), nil
}
func (s *Store) researchRequirementPage(ctx context.Context, actor string, f researchLiveFilter) (researchLivePage, error) {
	out := researchLivePage{Items: []researchLiveItem{}, Products: map[string]string{}, People: map[string]string{}, Page: f.Page, PageSize: f.PageSize, CheckedAt: time.Now().Unix(), QueryMode: "live_sources"}
	if f.Severity != "" || f.Resolution != "" || f.PersonRole == "resolved_by" {
		return out, nil
	} // Bug-only fields cannot match a requirement.
	z, c, identity, e := s.liveReadClient(ctx, f.Connection, actor, f.Mine)
	if e != nil {
		return out, e
	}
	out.ReadIdentity = identity
	products, e := zentaoProductIDs(c)
	if e != nil {
		return out, e
	}
	if f.Product == 0 && len(products) > 1 {
		return s.researchAllLivePage(ctx, actor, f)
	}
	if f.Product > 0 {
		if !liveProductAllowed(c, f.Product) {
			return out, fmt.Errorf("产品不在允许范围")
		}
		products = []int{f.Product}
	}
	if f.Mine && f.Relation == "" {
		f.Person = c.Username
		f.PersonRole = "assigned_to"
	}
	if f.Person != "" && f.PersonRole != "assigned_to" && f.PersonRole != "opened_by" && f.PersonRole != "closed_by" {
		return out, fmt.Errorf("需求人员关系支持指派给、创建人或关闭人")
	}
	type cursor struct {
		Product  int
		Page     int
		Offset   int
		Digest   string
		ViewPage int
	}
	filter := f
	filter.Cursor = ""
	filter.Page = 0
	digest := digestJSON([]any{actor, c.Endpoint, c.Username, c.Config, filter})
	pos := cursor{Page: 1, Digest: digest, ViewPage: f.Page}
	if f.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		if e != nil || len(b) > 2048 || json.Unmarshal(b, &pos) != nil || pos.Digest != digest || pos.ViewPage != f.Page || pos.Product < 0 || pos.Product >= len(products) || pos.Page < 1 || pos.Offset < 0 || pos.Offset >= 100 {
			return out, fmt.Errorf("需求查询游标已变化，请刷新第一页")
		}
	} else if f.Page > 1 {
		return out, fmt.Errorf("请使用需求下一页游标")
	}
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	z.resetLiveSearch()
	for reads := 0; pos.Product < len(products) && len(out.Items) < f.PageSize && reads < 5; reads++ {
		product := products[pos.Product]
		out.Products[strconv.Itoa(product)] = "产品 #" + strconv.Itoa(product)
		var native struct {
			Stories json.RawMessage   `json:"stories"`
			Users   map[string]string `json:"users"`
			Pager   struct {
				Page  sourceCode `json:"pageID"`
				Total sourceCode `json:"recTotal"`
			} `json:"pager"`
		}
		e = z.call(ctx, "product", "browse", url.Values{"productID": {strconv.Itoa(product)}, "branch": {"all"}, "browseType": {"all"}, "storyType": {"story"}, "orderBy": {liveOrder(f.WorkItemFilter)}, "recTotal": {"0"}, "recPerPage": {"100"}, "pageID": {strconv.Itoa(pos.Page)}}, nil, &native)
		if e != nil {
			return out, e
		}
		rows, e := decodeLiveBugs(native.Stories)
		if e != nil {
			return out, fmt.Errorf("来源未提供需求列表：%w", e)
		}
		sortLiveBugs(rows, liveOrder(f.WorkItemFilter))
		if len(rows) > 100 || pos.Offset > len(rows) {
			return out, fmt.Errorf("来源需求分页不一致，请刷新")
		}
		if page := fmt.Sprint(native.Pager.Page); page != "" && page != "0" && page != strconv.Itoa(pos.Page) {
			return out, fmt.Errorf("来源未按需求页码返回数据")
		}
		for k, v := range native.Users {
			out.People[k] = v
		}
		for pos.Offset < len(rows) && len(out.Items) < f.PageSize {
			row := rows[pos.Offset]
			pos.Offset++
			p, _ := strconv.Atoi(researchValue(row, "product"))
			if p != product || !liveProductAllowed(c, p) {
				continue
			}
			if !requirementMatches(row, f) {
				continue
			}
			item, e := s.projectLiveRequirement(c, z, row)
			if e != nil {
				return out, e
			}
			item.PersonLabels = issuePersonLabels(native.Users, item.AssignedTo, item.OpenedBy, item.ResolvedBy, item.ClosedBy)
			matches, e := s.requirementDeliveryMatches(&item, actor, f)
			if e != nil {
				return out, e
			}
			if matches {
				out.Items = append(out.Items, item)
			}
		}
		if pos.Offset == len(rows) {
			pos.Offset = 0
			if len(rows) < 100 {
				pos.Product++
				pos.Page = 1
			} else {
				pos.Page++
			}
		}
	}
	out.HasMore = pos.Product < len(products)
	out.Total = len(out.Items)
	out.TotalExact = false
	if out.HasMore {
		pos.ViewPage = f.Page + 1
		b, _ := json.Marshal(pos)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}
func requirementMatches(row map[string]any, f researchLiveFilter) bool {
	if f.Query != "" && researchValue(row, "id") != f.Query && !strings.Contains(strings.ToLower(researchValue(row, "title")), strings.ToLower(f.Query)) {
		return false
	}
	status := researchValue(row, "status")
	if f.Status != "" && !(f.Status == "open" && status != "closed") && status != f.Status {
		return false
	}
	if f.Priority != "" && researchValue(row, "pri") != f.Priority {
		return false
	}
	field := map[string]string{"assigned_to": "assignedTo", "opened_by": "openedBy", "closed_by": "closedBy"}[f.PersonRole]
	if f.Person != "" && researchValue(row, field) != f.Person {
		return false
	}
	timeField := "lastEditedDate"
	if f.TimeField == "closed" {
		timeField = "closedDate"
	}
	date := researchValue(row, timeField)
	if f.SourceStart != "" && date < f.SourceStart+" 00:00:00" || f.SourceEnd != "" && date > f.SourceEnd+" 23:59:59" {
		return false
	}
	return true
}

func (s *Store) requirementDeliveryMatches(item *researchLiveItem, actor string, f researchLiveFilter) (bool, error) {
	rows := []researchLiveItem{*item}
	if e := s.decorateResearchRelations(rows, actor); e != nil {
		return false, e
	}
	*item = rows[0]
	if f.Relation == "following" && !item.Following || f.Relation == "participating" && !item.Participating {
		return false, nil
	}
	if f.Relation == "related" {
		var n int
		if e := s.db.QueryRow(`SELECT COUNT(*) FROM work_items w WHERE w.id=? AND `+researchRelationClause("w", "related"), item.ID, actor).Scan(&n); e != nil {
			return false, e
		}
		if n == 0 {
			return false, nil
		}
	}
	if !liveIntentMatches(f.ReleaseRequired, item.ReleaseRequired) {
		return false, nil
	}
	if (f.Delivery != "" || f.MainCandidate) && item.ID > 0 {
		evidence, e := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: WorkItemFilter{PageSize: 100}, IDs: []int64{item.ID}, Environment: f.Environment, ProductionOnly: f.Delivery == "pending_release" || f.MainCandidate})
		if e != nil {
			return false, e
		}
		for _, v := range evidence.Items {
			if v.ID == item.ID {
				item.SuccessfulDelivery = v.SuccessfulDelivery
			}
		}
	}
	if f.MainCandidate && item.ExternalStatus == "closed" && item.SuccessfulDelivery || f.Delivery != "" && item.SuccessfulDelivery {
		return false, nil
	}
	return true, nil
}
