package deploy

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type researchLiveFilter struct {
	// Internal, already registered delivery scope. Never populated from URL IDs.
	RegisteredIDs []int64
	MainCandidate bool
	WorkItemFilter
	Connection                    string
	Product                       int
	Mine                          bool
	Environment, Delivery, Cursor string
}
type researchLiveItem struct {
	WorkItem
	ProductID          int    `json:"product_id,omitempty"`
	SourceUpdatedAt    string `json:"source_updated_at"`
	SuccessfulDelivery bool   `json:"successful_delivery"`
	Registered         bool   `json:"registered"`
}
type researchLivePage struct {
	Items               []researchLiveItem           `json:"items"`
	Total               int                          `json:"total"`
	TotalExact          bool                         `json:"total_exact"`
	Page                int                          `json:"page"`
	PageSize            int                          `json:"page_size"`
	HasMore             bool                         `json:"has_more"`
	NextCursor          string                       `json:"next_cursor,omitempty"`
	CheckedAt           int64                        `json:"checked_at"`
	ReadIdentity        string                       `json:"read_identity"`
	Products            map[string]string            `json:"products"`
	People              map[string]string            `json:"people"`
	ReleaseEnvironments []string                     `json:"release_environments,omitempty"`
	QueryMode           string                       `json:"query_mode,omitempty"`
	FieldLabels         map[string]map[string]string `json:"field_labels,omitempty"`
}

type liveSourcePage struct {
	Bugs     json.RawMessage   `json:"bugs"`
	Order    string            `json:"orderBy"`
	Products map[string]string `json:"products"`
	Users    map[string]string `json:"users"`
	Pager    struct {
		Total sourceCode `json:"recTotal"`
		Size  sourceCode `json:"recPerPage"`
		Page  sourceCode `json:"pageID"`
	} `json:"pager"`
}
type livePredicate struct{ Field, Operator, Value string }

func livePredicates(f WorkItemFilter) ([]livePredicate, error) {
	out := []livePredicate{}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	timeField := "lastEditedDate"
	if f.TimeField == "closed" {
		timeField = "closedDate"
	}
	if f.SourceStart != "" {
		out = append(out, livePredicate{timeField, ">=", f.SourceStart + " 00:00:00"})
	} else if f.TimeField == "closed" && f.SourceEnd != "" {
		out = append(out, livePredicate{timeField, ">=", "0001-01-01 00:00:00"})
	}
	if f.SourceEnd != "" {
		out = append(out, livePredicate{timeField, "<=", f.SourceEnd + " 23:59:59"})
	}
	if f.Query != "" {
		if _, err := strconv.ParseInt(f.Query, 10, 64); err == nil {
			out = append(out, livePredicate{"id", "=", f.Query})
		} else {
			out = append(out, livePredicate{"title", "include", f.Query})
		}
	}
	if f.Status != "" {
		op, value := "=", f.Status
		if value == "open" {
			op, value = "!=", "closed"
		}
		out = append(out, livePredicate{"status", op, value})
	}
	if f.Person != "" {
		field := map[string]string{"assigned_to": "assignedTo", "opened_by": "openedBy", "resolved_by": "resolvedBy", "closed_by": "closedBy"}[f.PersonRole]
		if field == "" {
			return nil, fmt.Errorf("实时查询请指定人员关系：指派给、解决人、创建人或关闭人")
		}
		out = append(out, livePredicate{field, "=", f.Person})
	}
	for _, v := range []struct{ Field, Value string }{{"severity", f.Severity}, {"pri", f.Priority}, {"resolution", f.Resolution}} {
		if v.Value != "" {
			out = append(out, livePredicate{v.Field, "=", v.Value})
		}
	}
	return out, nil
}
func liveOrder(f WorkItemFilter) string {
	switch f.Order {
	case "severity_asc", "severity_desc", "resolution_asc", "resolution_desc":
		return f.Order
	case "priority_asc":
		return "pri_asc"
	case "priority_desc":
		return "pri_desc"
	case "updated_asc":
		return "lastEditedDate_asc"
	default:
		return "lastEditedDate_desc"
	}
}
func decodeLiveBugs(raw json.RawMessage) ([]map[string]any, error) {
	decode := func(v any) error {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		return d.Decode(v)
	}
	var rows []map[string]any
	if decode(&rows) != nil {
		var indexed map[string]map[string]any
		if decode(&indexed) != nil || indexed == nil {
			return nil, fmt.Errorf("来源未返回有效 Bug 列表")
		}
		for _, row := range indexed {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func sortLiveBugs(rows []map[string]any, order string) {
	parts := strings.Split(order, "_")
	field := parts[0]
	descending := parts[len(parts)-1] == "desc"
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := researchValue(rows[i], field), researchValue(rows[j], field)
		if a == b {
			ai, _ := strconv.ParseInt(researchValue(rows[i], "id"), 10, 64)
			bi, _ := strconv.ParseInt(researchValue(rows[j], "id"), 10, 64)
			return ai > bi
		}
		if field == "severity" || field == "pri" {
			ai, _ := strconv.Atoi(a)
			bi, _ := strconv.Atoi(b)
			if descending {
				return ai > bi
			}
			return ai < bi
		}
		if descending {
			return a > b
		}
		return a < b
	})
}
func (z *zentaoLegacy) liveBrowse(ctx context.Context, product, page, size int, browse, order string) (liveSourcePage, []map[string]any, error) {
	var out liveSourcePage
	err := z.call(ctx, "bug", "browse", url.Values{"productID": {strconv.Itoa(product)}, "branch": {"all"}, "browseType": {browse}, "param": {"0"}, "orderBy": {order}, "recTotal": {"0"}, "recPerPage": {strconv.Itoa(size)}, "pageID": {strconv.Itoa(page)}}, nil, &out)
	if err != nil {
		return out, nil, err
	}
	rows, err := decodeLiveBugs(out.Bugs)
	if err != nil {
		return out, nil, err
	}
	total, e1 := strconv.Atoi(string(out.Pager.Total))
	actualSize, e2 := strconv.Atoi(string(out.Pager.Size))
	actualPage, e3 := strconv.Atoi(string(out.Pager.Page))
	if e1 != nil || e2 != nil || e3 != nil || total < 0 || actualSize != size || actualPage < 1 || len(rows) > size || out.Order != order {
		return out, nil, fmt.Errorf("来源分页或排序信息不兼容，未改用旧数据")
	}
	if actualPage != page {
		return out, nil, fmt.Errorf("来源数据或页码已变化，请重新查询第一页")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		id, err := strconv.ParseInt(researchValue(row, "id"), 10, 64)
		if err != nil || id <= 0 || seen[strconv.FormatInt(id, 10)] || researchValue(row, "title") == "" {
			return out, nil, fmt.Errorf("来源返回无效或重复 Bug")
		}
		seen[strconv.FormatInt(id, 10)] = true
		if p := researchValue(row, "product"); product > 0 && p != "" && p != strconv.Itoa(product) {
			return out, nil, fmt.Errorf("来源返回超出所选产品的数据")
		}
	}
	sortLiveBugs(rows, order)
	return out, rows, nil
}

// A full search sequence is serialized on the session: one tab's filters must
// never leak into another tab's query. No user-supplied SQL or method names.
const liveSearchSetupTTL = 5 * time.Minute

// Caller holds queryMu. This caches upstream search state, not returned data.
func (z *zentaoLegacy) resetLiveSearch() {
	z.searchProduct, z.searchGroupItems = 0, 0
	z.searchInitializedAt, z.searchCompiledAt = time.Time{}, time.Time{}
	z.searchDigest = ""
}

func (z *zentaoLegacy) prepareLiveQuery(ctx context.Context, product, size int, f WorkItemFilter, allProducts ...bool) (string, error) {
	root, _ := url.Parse(z.root + "/")
	z.client.Jar.SetCookies(root, []*http.Cookie{{Name: "treeBranch", Value: "all", Path: "/"}})
	predicates, err := livePredicates(f)
	if err != nil {
		return "", err
	}
	if len(allProducts) > 0 && allProducts[0] {
		predicates = append(predicates, livePredicate{"product", "=", "all"})
	}
	if len(predicates) == 0 {
		// Plain browse may replace native product/search parameters.
		z.resetLiveSearch()
		return "all", nil
	}
	if z.searchProduct != product || time.Since(z.searchInitializedAt) >= liveSearchSetupTTL {
		z.resetLiveSearch()
		// Initialize once per product/session, not on every page or filter.
		if _, _, err = z.liveBrowse(ctx, product, 1, size, "all", liveOrder(f)); err != nil {
			return "", err
		}
		var formConfig struct {
			GroupItems int `json:"groupItems"`
		}
		if err = z.call(ctx, "search", "buildForm", url.Values{"module": {"bug"}}, nil, &formConfig); err != nil {
			return "", err
		}
		if formConfig.GroupItems < 1 || formConfig.GroupItems > 20 {
			return "", fmt.Errorf("来源搜索配置不兼容")
		}
		z.searchProduct, z.searchGroupItems, z.searchInitializedAt = product, formConfig.GroupItems, time.Now()
	}
	if len(predicates) > 2*z.searchGroupItems {
		return "", fmt.Errorf("来源搜索最多支持 %d 个组合条件，请减少条件", 2*z.searchGroupItems)
	}
	// Order, page and size are sent on every live browse. Only WHERE predicates
	// belong to the compiled query; multiple windows replace it under queryMu.
	queryDigest := digestJSON([]any{product, predicates})
	if z.searchDigest == queryDigest && time.Since(z.searchCompiledAt) < liveSearchSetupTTL {
		return "bysearch", nil
	}
	args := url.Values{"productID": {strconv.Itoa(product)}, "branch": {"all"}, "browseType": {"bysearch"}, "param": {"0"}, "orderBy": {liveOrder(f)}, "recTotal": {"0"}, "recPerPage": {strconv.Itoa(size)}, "pageID": {"1"}}
	address, _ := z.requestURL("bug", "browse", args)
	action, _ := url.Parse(address)
	actionURI := action.RequestURI()
	if !z.pathInfo {
		actionURI = action.Path + "?m=bug&f=browse&t=json&" + args.Encode()
	}
	form := url.Values{"module": {"bug"}, "groupAndOr": {"and"}, "actionURL": {actionURI}}
	for i := 1; i <= 2*z.searchGroupItems; i++ {
		suffix := strconv.Itoa(i)
		form.Set("field"+suffix, "id")
		form.Set("operator"+suffix, "=")
		form.Set("value"+suffix, "")
		form.Set("andOr"+suffix, "and")
		if i <= len(predicates) {
			p := predicates[i-1]
			form.Set("field"+suffix, p.Field)
			form.Set("operator"+suffix, p.Operator)
			form.Set("value"+suffix, p.Value)
		}
	}
	if err = z.call(ctx, "search", "buildQuery", nil, form, &struct{}{}); err != nil {
		return "", err
	}
	z.searchDigest, z.searchCompiledAt = queryDigest, time.Now()
	return "bysearch", nil
}

func (s *Store) liveReadClient(ctx context.Context, name, actor string, requirePersonal bool) (*zentaoLegacy, *ResourceConnection, string, error) {
	c := s.GetConnection(name)
	if c == nil || c.Kind != "zentao" {
		return nil, nil, "", fmt.Errorf("来源连接不存在")
	}
	var configuredActor string
	err := s.db.QueryRow(`SELECT actor FROM research_credentials WHERE actor=? AND connection_name=?`, actor, name).Scan(&configuredActor)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, "", err
	}
	if configuredActor != "" && configuredActor == actor && actor != "local" {
		c, v, fp, err := s.personalZentaoCredentials(name, actor)
		if err != nil {
			return nil, nil, "", err
		}
		c.Username, c.Password = v.Account, v.Password
		z, err := s.cachedZentao(ctx, c, actor, fp)
		return z, c, "personal", err
	}
	if requirePersonal {
		return nil, nil, "", &zentaoAuthenticationError{"我的筛选需要先验证本人来源账号；不能将公共读取账号当作本人"}
	}
	z, err := s.connectionZentao(ctx, c)
	return z, c, "public", err
}
func liveProductAllowed(c *ResourceConnection, product int) bool {
	ids, err := zentaoProductIDs(c)
	if err != nil {
		return false
	}
	for _, id := range ids {
		if id == product {
			return true
		}
	}
	return false
}

func (s *Store) liveLocalReference(name, id string) (*WorkItem, error) {
	var localID int64
	err := s.db.QueryRow(`SELECT work_item_id FROM external_work_items WHERE connection=? AND kind='bug' AND external_id=?`, name, id).Scan(&localID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	local := s.GetWorkItem(localID)
	if local == nil || local.Source != "zentao:"+name || local.ExternalID != id {
		return nil, fmt.Errorf("来源交付关联异常，请核对历史登记")
	}
	local.ReleaseRequired = nil
	if err = s.db.QueryRow(`SELECT release_required FROM research_work_metadata WHERE work_item_id=?`, localID).Scan(&local.ReleaseRequired); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return local, nil
}
func (s *Store) projectLiveBug(c *ResourceConnection, z *zentaoLegacy, row map[string]any, now int64, reference ...*WorkItem) (researchLiveItem, error) {
	id := researchValue(row, "id")
	var local *WorkItem
	if len(reference) > 0 {
		local = reference[0]
	} else {
		var err error
		local, err = s.liveLocalReference(c.Name, id)
		if err != nil {
			return researchLiveItem{}, err
		}
	}
	item := researchLiveItem{WorkItem: WorkItem{Source: "zentao:" + c.Name, ExternalID: id, Kind: "bug", Title: researchValue(row, "title"), URL: z.bugURL(id), ExternalStatus: researchValue(row, "status"), Status: WorkItemOpen, AssignedTo: researchValue(row, "assignedTo"), OpenedBy: researchValue(row, "openedBy"), ResolvedBy: researchValue(row, "resolvedBy"), ClosedBy: researchValue(row, "closedBy")}, SourceUpdatedAt: researchValue(row, "lastEditedDate")}
	item.ResearchIssueFields = issueFields(c, researchValue(row, "severity"), researchValue(row, "pri"), researchValue(row, "resolution"), researchValue(row, "closedDate"))
	item.ProjectID = researchValue(row, "project")
	item.SourceProductID = researchValue(row, "product")
	item.ReadAt = now
	if item.ExternalStatus == "closed" {
		item.Status = WorkItemClosed
	}
	if local != nil {
		if !strings.HasPrefix(local.URL, z.root+"/") {
			return item, fmt.Errorf("来源安装地址已变化，请先核对原交付关联")
		}
		item.ID, item.Owner, item.ReleaseRequired, item.NoReleaseReason, item.MetadataRevision = local.ID, local.Owner, local.ReleaseRequired, local.NoReleaseReason, local.MetadataRevision
		item.Registered = true
	}
	rules, err := s.researchReleaseRules(c.Name)
	if err != nil {
		return item, err
	}
	applyResearchReleaseRules(&item.WorkItem, rules, item.ProjectID)
	return item, nil
}
func liveIntentMatches(intent string, value *bool) bool {
	switch intent {
	case "yes":
		return value != nil && *value
	case "no":
		return value != nil && !*value
	case "unknown":
		return value == nil
	case "eligible":
		return value == nil || *value
	default:
		return true
	}
}

type liveCursor struct {
	Page, Index int
	Digest      string
}

func (s *Store) ResearchLivePage(ctx context.Context, actor string, f researchLiveFilter) (out researchLivePage, resultErr error) {
	out = researchLivePage{Items: []researchLiveItem{}, Products: map[string]string{}, People: map[string]string{}, TotalExact: true, CheckedAt: time.Now().Unix()}
	defer func() {
		if resultErr == nil {
			resultErr = s.decorateResearchRelations(out.Items, actor)
			if resultErr == nil {
				resultErr = s.decorateResearchReleaseMarks(out.Items)
			}
		}
	}()
	if err := f.Validate(); err != nil {
		return out, err
	}
	if f.Connection == "" {
		return s.researchAllLivePage(ctx, actor, f)
	}
	productionOnly := f.Delivery == "pending_release"
	if productionOnly {
		f.ReleaseRequired = "yes"
		f.Environment = "" // The caller cannot redefine上线 as a testing environment.
		for _, e := range s.Environments() {
			if e.Purpose == "production" {
				out.ReleaseEnvironments = append(out.ReleaseEnvironments, e.Name)
			}
		}
		if len(out.ReleaseEnvironments) == 0 {
			return out, fmt.Errorf("尚未配置生产用途的环境，请管理员在环境管理中指定用途")
		}
	}
	if f.Connection == "manual" {
		f.WorkItemFilter.Source = "manual"
		if f.Status == "active" {
			f.Status = "open"
		}
		if f.Mine && f.Relation == "" {
			f.Person = actor
			if f.PersonRole == "" {
				f.PersonRole = "assigned_to"
			}
		}
		if f.Delivery != "" || f.MainCandidate {
			delivery := f.Delivery
			if productionOnly || f.MainCandidate {
				delivery = "not_recorded"
			}
			candidate, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: f.WorkItemFilter, Source: "manual", Environment: f.Environment, Delivery: delivery, ProductionOnly: productionOnly || f.MainCandidate, ExcludeManualRelease: productionOnly})
			if err != nil {
				return out, err
			}
			for _, c := range candidate.Items {
				if item := s.GetWorkItem(c.ID); item != nil {
					out.Items = append(out.Items, researchLiveItem{WorkItem: *item, Registered: true, SuccessfulDelivery: c.SuccessfulDelivery})
				}
			}
			out.Total, out.Page, out.PageSize = candidate.Total, candidate.Page, candidate.PageSize
			out.HasMore = out.Page*out.PageSize < out.Total
			out.ReadIdentity = "platform"
			return out, nil
		}
		local, err := s.WorkItemsPage(f.WorkItemFilter)
		if err != nil {
			return out, err
		}
		for _, item := range local.Items {
			out.Items = append(out.Items, researchLiveItem{WorkItem: item, Registered: true})
		}
		out.Page, out.PageSize, out.Total = local.Page, local.PageSize, local.Total
		out.HasMore = local.Page*local.PageSize < local.Total
		out.ReadIdentity = "platform"
		return out, nil
	}

	if f.Kind == "requirement" {
		return s.researchRequirementPage(ctx, actor, f)
	}
	if f.Kind != "" && f.Kind != "bug" {
		return out, fmt.Errorf("此来源当前提供 Bug 查询；自定义需求请选择自定义工作项")
	}
	if f.Delivery != "" && !productionOnly && (f.Delivery != "not_recorded" || f.Environment == "" || s.GetEnvironment(f.Environment) == nil) {
		return out, fmt.Errorf("待上线查询需指定有效核对环境")
	}
	z, c, identity, err := s.liveReadClient(ctx, f.Connection, actor, f.Mine)
	if err != nil {
		return out, err
	}
	defer func() {
		if resultErr == nil {
			z.enrichLiveResolutionLabels(ctx, c, &out)
			s.rememberResearchPeople(actor, c, out.People)
		}
	}()
	ids, err := zentaoProductIDs(c)
	if err != nil {
		return out, fmt.Errorf("请管理员在来源连接中配置可查询产品 ID")
	}
	if f.Product < 0 || f.Product > 0 && !liveProductAllowed(c, f.Product) {
		return out, fmt.Errorf("所选产品不在来源连接的允许范围")
	}
	if f.Mine && f.Relation == "" {
		f.Person = c.Username
		if f.PersonRole == "" || f.PersonRole == "related" {
			f.PersonRole = "assigned_to"
		}
	}
	out.ReadIdentity, out.Page, out.PageSize = identity, f.Page, f.PageSize
	for _, id := range ids {
		out.Products[strconv.Itoa(id)] = "产品 #" + strconv.Itoa(id)
	}
	// All-products is native search, with an additional platform allowlist guard.
	// Its candidate total is not misrepresented as an exact scoped total.
	localFilter := f.Product == 0 || f.ReleaseRequired != "" || f.Delivery != "" || f.Relation != "" || f.MainCandidate
	out.TotalExact = !localFilter
	rules, err := s.researchReleaseRules(c.Name)
	if err != nil {
		return out, err
	}
	digest := digestJSON([]any{actor, c.Endpoint, c.Username, ids, f.Connection, f.Product, f.Query, f.Status, f.Person, f.PersonRole, f.Severity, f.Priority, f.Resolution, f.Order, f.PageSize, f.ReleaseRequired, f.Environment, f.Delivery, out.ReleaseEnvironments, f.Relation, rules.Revision, f.MainCandidate, f.TimeField, f.SourceStart, f.SourceEnd})
	if productionOnly && !rules.Enabled || f.Relation != "" {
		batch, handled, err := s.livePendingRelease(ctx, z, c, f, out, digest)
		if handled || err != nil {
			return batch, err
		}
	}
	position := liveCursor{Page: f.Page, Digest: digest}
	if localFilter {
		position.Page = 1
		if f.Page > 1 && f.Cursor == "" {
			return out, fmt.Errorf("组合交付条件需使用下一页游标，不能直接跳页")
		}
	}
	if f.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		if e != nil || len(raw) > 512 || json.Unmarshal(raw, &position) != nil || position.Digest != digest || position.Page < 1 || position.Page > 1000000 || position.Index < 0 || position.Index >= f.PageSize {
			return out, fmt.Errorf("查询条件或游标已变化，请重新查询第一页")
		}
	}
	z.queryMu.Lock()
	queryOK := false
	defer func() {
		if !queryOK {
			z.resetLiveSearch()
		}
		z.queryMu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	searchProduct := f.Product
	if searchProduct == 0 {
		searchProduct = ids[0]
	}
	browse, err := z.prepareLiveQuery(ctx, searchProduct, f.PageSize, f.WorkItemFilter, f.Product == 0)
	if err != nil {
		return out, err
	}
	for scan := 0; scan < 10; scan++ {
		native, rows, err := z.liveBrowse(ctx, f.Product, position.Page, f.PageSize, browse, liveOrder(f.WorkItemFilter))
		if err != nil {
			return out, err
		}
		out.Total, _ = strconv.Atoi(string(native.Pager.Total))
		for _, id := range ids {
			key := strconv.Itoa(id)
			if native.Products[key] != "" {
				out.Products[key] = native.Products[key]
			}
		}
		out.People = native.Users
		predicates, _ := livePredicates(f.WorkItemFilter)
		projected := []researchLiveItem{}
		localIDs := []int64{}
		referenceIDs := []string{}
		for _, row := range rows {
			product, _ := strconv.Atoi(researchValue(row, "product"))
			if liveProductAllowed(c, product) {
				referenceIDs = append(referenceIDs, researchValue(row, "id"))
			}
		}
		references, err := s.liveLocalReferences(ctx, c.Name, referenceIDs)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			product, _ := strconv.Atoi(researchValue(row, "product"))
			if !liveProductAllowed(c, product) {
				// Keep a position for the native cursor, but never project/expose data
				// outside the configured platform product scope.
				projected = append(projected, researchLiveItem{})
				continue
			}
			for _, p := range predicates {
				value := researchValue(row, p.Field)
				if (p.Operator == "=" && value != p.Value) || (p.Operator == "!=" && value == p.Value) || (p.Operator == ">=" && value < p.Value) || (p.Operator == "<=" && value > p.Value) || (p.Operator == "include" && !strings.Contains(strings.ToLower(value), strings.ToLower(p.Value))) {
					return out, fmt.Errorf("来源未正确应用筛选条件，未展示未筛选数据")
				}
			}
			v, err := s.projectLiveBug(c, z, row, out.CheckedAt, references[researchValue(row, "id")])
			if err != nil {
				return out, err
			}
			v.ProductID = product
			v.PersonLabels = issuePersonLabels(out.People, v.AssignedTo, v.OpenedBy, v.ResolvedBy, v.ClosedBy)
			projected = append(projected, v)
			if v.ID > 0 {
				localIDs = append(localIDs, v.ID)
			}
		}
		successful := map[int64]bool{}
		if err = s.decorateResearchReleaseMarks(projected); err != nil {
			return out, err
		}
		if err = s.decorateResearchRelations(projected, actor); err != nil {
			return out, err
		}
		if (f.Delivery != "" || f.MainCandidate) && len(localIDs) > 0 {
			evidence, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: WorkItemFilter{PageSize: 100}, IDs: localIDs, Environment: f.Environment, ProductionOnly: productionOnly || f.MainCandidate})
			if err != nil {
				return out, err
			}
			for _, v := range evidence.Items {
				successful[v.ID] = v.SuccessfulDelivery
			}
		}
		index := position.Index
		for index < len(projected) {
			v := projected[index]
			index++
			if v.Source == "" {
				continue
			}
			v.SuccessfulDelivery = successful[v.ID]
			if f.MainCandidate && v.ExternalStatus == "closed" && v.SuccessfulDelivery {
				continue
			}
			if f.Relation == "following" && !v.Following || f.Relation == "participating" && !v.Participating {
				continue
			}
			if f.Relation == "related" {
				var n int
				if err = s.db.QueryRow(`SELECT COUNT(*) FROM work_items w WHERE w.id=? AND `+researchRelationClause("w", "related"), v.ID, actor).Scan(&n); err != nil {
					return out, err
				}
				if n == 0 {
					continue
				}
			}
			if !liveIntentMatches(f.ReleaseRequired, v.ReleaseRequired) || (f.Delivery != "" && v.SuccessfulDelivery) || (productionOnly && manuallyMarkedReleased(v.WorkItem)) {
				continue
			}
			out.Items = append(out.Items, v)
			if len(out.Items) >= f.PageSize {
				break
			}
		}
		more := index < len(projected) || position.Page*f.PageSize < out.Total
		out.HasMore = more
		if index >= len(projected) {
			position.Page++
			position.Index = 0
		} else {
			position.Index = index
		}
		if !localFilter || !more || len(out.Items) >= f.PageSize || scan == 9 {
			if more && localFilter {
				raw, _ := json.Marshal(position)
				out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			}
			queryOK = true
			return out, nil
		}
	}
	queryOK = true
	return out, nil
}

// One metadata query for a native page. No persisted Bug-body cache and no
// N+1 lookups; broken or crossed-source references still fail closed.
func (s *Store) liveLocalReferences(ctx context.Context, name string, ids []string) (map[string]*WorkItem, error) {
	out := map[string]*WorkItem{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{name}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.external_id,e.work_item_id,w.id,w.source,w.external_id,w.kind,COALESCE(w.url,''),COALESCE(w.owner,''),m.release_required,COALESCE(m.no_release_reason,''),COALESCE(m.revision,0) FROM external_work_items e LEFT JOIN work_items w ON w.id=e.work_item_id LEFT JOIN research_work_metadata m ON m.work_item_id=w.id WHERE e.connection=? AND e.kind='bug' AND e.external_id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var external string
		var linked int64
		var id sql.NullInt64
		var source, number, kind sql.NullString
		var item WorkItem
		if err = rows.Scan(&external, &linked, &id, &source, &number, &kind, &item.URL, &item.Owner, &item.ReleaseRequired, &item.NoReleaseReason, &item.MetadataRevision); err != nil {
			return out, err
		}
		if !id.Valid || id.Int64 != linked || source.String != "zentao:"+name || number.String != external || kind.String != "bug" {
			return out, fmt.Errorf("来源交付关联异常，请核对历史登记")
		}
		item.ID, item.Source, item.ExternalID, item.Kind = id.Int64, source.String, number.String, kind.String
		out[external] = &item
	}
	return out, rows.Err()
}

// Explicitly selected Bug only. Register a stable platform reference and a
// delivery snapshot; page reads never import/update all upstream Bug records.
func (s *Store) AttachResearchBug(ctx context.Context, actor, name, id string) (*WorkItem, error) {
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil || number <= 0 {
		return nil, fmt.Errorf("来源 Bug 编号无效")
	}
	z, c, _, err := s.liveReadClient(ctx, name, actor, false)
	if err != nil {
		return nil, err
	}
	remote, err := z.researchRead(ctx, id, "view")
	if err != nil {
		return nil, err
	}
	product, _ := strconv.Atoi(researchValue(remote.Bug, "product"))
	if !liveProductAllowed(c, product) {
		return nil, fmt.Errorf("Bug 不在连接允许的产品范围")
	}
	projected, err := s.projectLiveBug(c, z, remote.Bug, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	projected.PersonLabels = issuePersonLabels(remote.Users, projected.AssignedTo, projected.OpenedBy, projected.ResolvedBy, projected.ClosedBy)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	localID := projected.ID
	now := time.Now().Unix()
	if localID == 0 {
		result, err := tx.Exec(`INSERT INTO work_items(source,external_id,kind,title,url,external_status,description,status,owner,created_at,updated_at,assigned_to,opened_by,resolved_by,closed_by,description_format) VALUES(?,?,'bug',?,?,?,?,?,?,?,?,?,?,?,?,'html')`, projected.Source, id, projected.Title, projected.URL, projected.ExternalStatus, researchValue(remote.Bug, "steps"), projected.Status, actor, now, now, projected.AssignedTo, projected.OpenedBy, projected.ResolvedBy, projected.ClosedBy)
		if err != nil {
			return nil, err
		}
		localID, err = result.LastInsertId()
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO external_work_items(connection,kind,external_id,work_item_id) VALUES(?,'bug',?,?)`, name, id, localID); err != nil {
			return nil, fmt.Errorf("交付关联已由另一请求登记，请重新打开；未重复保存")
		}
	} else {
		if _, err = tx.Exec(`UPDATE work_items SET title=?,url=?,external_status=?,description=?,status=?,updated_at=?,assigned_to=?,opened_by=?,resolved_by=?,closed_by=? WHERE id=?`, projected.Title, projected.URL, projected.ExternalStatus, researchValue(remote.Bug, "steps"), projected.Status, now, projected.AssignedTo, projected.OpenedBy, projected.ResolvedBy, projected.ClosedBy, localID); err != nil {
			return nil, err
		}
	}
	if err = saveIssueFields(tx, localID, projected.ResearchIssueFields); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetWorkItem(localID), nil
}

func registerResearchLiveRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/research/work-items/sources", func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{}
		for _, c := range s.Connections() {
			if c.Kind != "zentao" {
				continue
			}
			ids, err := zentaoProductIDs(&c)
			if err != nil {
				ids = []int{}
			}
			out = append(out, map[string]any{"name": c.Name, "kind": "bug", "live": true, "products": ids})
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/research/work-items/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := researchLiveFilter{Connection: q.Get("connection"), Mine: q.Get("mine") == "1", Environment: q.Get("environment"), Delivery: q.Get("delivery"), Cursor: q.Get("cursor"), WorkItemFilter: WorkItemFilter{Query: q.Get("q"), Status: q.Get("status"), Kind: q.Get("kind"), Person: q.Get("person"), PersonRole: q.Get("person_role"), ReleaseRequired: q.Get("release_required"), Severity: q.Get("severity"), Priority: q.Get("priority"), Resolution: q.Get("resolution"), Order: q.Get("order")}}
		f.Relation, f.RelationActor = q.Get("relation"), deliveryActor(r)
		f.MainCandidate = q.Get("main_candidate") == "1"
		f.TimeField, f.SourceStart, f.SourceEnd = q.Get("time_field"), q.Get("source_start"), q.Get("source_end")
		for key, dst := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize, "product": &f.Product} {
			if value := q.Get(key); value != "" {
				v, err := strconv.Atoi(value)
				if err != nil || v < 0 || (key != "product" && v < 1) {
					deliveryWriteError(w, 400, "分页或产品参数无效")
					return
				}
				*dst = v
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		out, err := s.ResearchLivePage(ctx, deliveryActor(r), f)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("POST /api/research/work-items/attach", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Connection string `json:"connection"`
			ExternalID string `json:"external_id"`
			Kind       string `json:"kind"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "关联参数无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		var item *WorkItem
		var err error
		if req.Kind == "requirement" {
			item, err = s.AttachResearchRequirement(ctx, deliveryActor(r), req.Connection, req.ExternalID)
		} else if req.Kind == "" || req.Kind == "bug" {
			item, err = s.AttachResearchBug(ctx, deliveryActor(r), req.Connection, req.ExternalID)
		} else {
			err = fmt.Errorf("工作项类型无效")
		}
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, item)
	})
}
