package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type apiTestCell struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type apiTestDataRow struct {
	Name   string                 `json:"name"`
	Values map[string]apiTestCell `json:"values"`
}
type apiTestSuite struct {
	Bindings   []apiTestCaseBinding `json:"bindings,omitempty"`
	ID         int64                `json:"id"`
	Revision   int64                `json:"revision"`
	Name       string               `json:"name"`
	Owner      string               `json:"owner"`
	Visibility string               `json:"visibility"`
	CanEdit    bool                 `json:"can_edit"`
	CaseIDs    []int64              `json:"case_ids"`
}
type apiTestUnit struct {
	CaseSnapshot       apiTestCase `json:"case_snapshot"`
	RowName            string      `json:"row_name"`
	DocumentDigest     string      `json:"document_digest,omitempty"`
	DocumentArtifactID int64       `json:"document_artifact_id,omitempty"`
	DocumentCommit     string      `json:"document_commit,omitempty"`
	Start              int         `json:"start"`
	End                int         `json:"end"`
	Status             string      `json:"status"`
}
type apiTestPlannedUnit struct {
	Case    apiTestCase
	Data    map[string]any
	RowName string
}

var apiTestColumnRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func apiTestDataScope(row apiTestDataRow) (map[string]any, error) {
	if strings.TrimSpace(row.Name) == "" || len(row.Name) > 200 || len(row.Values) == 0 || len(row.Values) > 20 {
		return nil, fmt.Errorf("每组数据需要名称和 1–20 个数据列")
	}
	scope := map[string]any{}

	for key, cell := range row.Values {
		if !apiTestColumnRE.MatchString(key) || len(cell.Value) > 8192 {
			return nil, fmt.Errorf("数据列名称无效或单元格超过 8 KiB")
		}
		var value any = cell.Value
		switch cell.Type {
		case "string":
		case "uuid":
			if cell.Value != "" {
				return nil, fmt.Errorf("UUID 数据列 %s 由执行器生成，请留空", key)
			}
		case "null":
			value = nil
		case "number", "boolean":
			d := json.NewDecoder(strings.NewReader(cell.Value))
			d.UseNumber()
			if !json.Valid([]byte(cell.Value)) || d.Decode(&value) != nil {
				return nil, fmt.Errorf("数据列 %s 的类型与值不符", key)
			}
			if cell.Type == "number" {
				if _, ok := value.(json.Number); !ok {
					return nil, fmt.Errorf("数据列 %s 需要数字", key)
				}
			} else {
				if _, ok := value.(bool); !ok {
					return nil, fmt.Errorf("数据列 %s 需要布尔值", key)
				}
			}
		default:
			return nil, fmt.Errorf("数据列类型无效")
		}
		scope["data."+key] = value
	}

	return scope, nil
}

func runAPITestDataScope(row apiTestDataRow) (map[string]any, error) {
	scope, err := apiTestDataScope(row)
	if err != nil {
		return nil, err
	}
	for name, cell := range row.Values {
		if cell.Type == "uuid" {
			id, err := uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("生成 UUID 失败: %w", err)
			}
			scope["data."+name] = id.String()
		}
	}
	return scope, nil
}
func validateAPITestData(v *apiTestCase) error {
	if len(v.DataRows)*len(v.Steps) > 100 {
		return fmt.Errorf("数据组与步骤合计超过 100 步，请拆分用例")
	}
	if len(v.DataRows) > 20 {
		return fmt.Errorf("每个用例最多 20 组数据")
	}
	rows := v.DataRows
	if len(rows) == 0 {
		rows = []apiTestDataRow{{}}
	}
	names := map[string]bool{}
	for _, row := range rows {
		scope := map[string]any{}
		if len(v.DataRows) > 0 {
			var e error
			scope, e = apiTestDataScope(row)
			if e != nil {
				return e
			}
			if names[row.Name] {
				return fmt.Errorf("数据组名称不能重复")
			}
			names[row.Name] = true
		}
		for i, step := range v.Steps {
			if step.Condition != nil && strings.HasPrefix(step.Condition.Variable, "data.") {
				if _, ok := scope[step.Condition.Variable]; !ok {
					return fmt.Errorf("条件数据变量 %s 未配置", step.Condition.Variable)
				}
			}
			if e := apitest.ValidateDataReferences(step.Request, scope); e != nil {
				return fmt.Errorf("数据组 %s，步骤 %d：%w", row.Name, i+1, e)
			}
		}
	}
	return nil
}
func caseCanEdit(c *apiTestCase, owner string, admin bool) bool {
	return c.Owner == owner || admin && c.Visibility == "team"
}
func (s *Store) apiTestSuite(app, owner string, id int64, admin bool) (*apiTestSuite, error) {
	var enc, who, visibility string
	var revision int64
	if e := s.db.QueryRow(`SELECT document,owner,visibility,revision FROM api_test_suites WHERE id=? AND application_id=? AND deleted=0 AND (owner=? OR visibility='team')`, id, app, owner).Scan(&enc, &who, &visibility, &revision); e != nil {
		return nil, fmt.Errorf("测试集不存在或无权访问")
	}
	raw, e := s.decryptSecret(enc)
	if e != nil {
		return nil, e
	}
	var v apiTestSuite
	if e = json.Unmarshal([]byte(raw), &v); e != nil {
		return nil, e
	}
	v.ID = id
	v.Revision = revision
	v.Owner = who
	v.Visibility = visibility
	v.CanEdit = owner == who || admin && visibility == "team"
	return &v, nil
}
func registerAPITestSuites(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, admin func(*http.Request) bool, scope func(http.ResponseWriter, *http.Request) *Service, decode func(http.ResponseWriter, *http.Request, any) bool) {
	mux.HandleFunc("GET /api/api-testing/suites", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		rows, e := st.db.Query(`SELECT id FROM api_test_suites WHERE application_id=? AND deleted=0 AND (owner=? OR visibility='team') ORDER BY id DESC`, sv.ApplicationID, actor(r))
		if e != nil {
			fail(w, 500, e)
			return
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		out := []apiTestSuite{}
		for _, id := range ids {
			if v, e := st.apiTestSuite(sv.ApplicationID, actor(r), id, admin(r)); e == nil {
				out = append(out, *v)
			}
		}
		wj(w, out)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		var v apiTestSuite
		var expected map[string]int64
		mcp := strings.Contains(r.URL.Path, "/mcp/")
		if mcp {
			var in apiTestSuiteMutation
			if !decode(w, r, &in) {
				return
			}
			v, expected = in.Suite, in.CaseRevisions
			if actor(r) == "" || actor(r) == "local" || st.GetServiceEnvironment(sv.Name, r.URL.Query().Get("environment")) == nil {
				fail(w, 403, "需要已绑定用户和应用环境")
				return
			}
		} else if !decode(w, r, &v) {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if mcp && v.ID != id {
			fail(w, 409, "测试集编号不匹配")
			return
		}
		v.ID = id
		cases, err := st.validateAPITestSuite(&v, sv.ApplicationID, actor(r), admin(r))
		if err != nil {
			fail(w, apiTestSuiteErrorStatus(err), err)
			return
		}
		if mcp {
			if len(expected) != len(cases) {
				fail(w, 409, "必须携带全部用例的冻结修订")
				return
			}
			for _, c := range cases {
				if expected[strconv.FormatInt(c.ID, 10)] != c.Revision {
					fail(w, 409, "测试集中的用例已变化，请重新预览")
					return
				}
			}
		}
		oldRevision := v.Revision
		v.Revision++
		v.ID = id
		v.CanEdit = true
		raw, _ := json.Marshal(v)
		enc, e := st.encryptSecret(string(raw))
		if e != nil {
			fail(w, 500, e)
			return
		}
		tx, e := st.db.Begin()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer tx.Rollback()
		if mcp {
			for _, c := range cases {
				var revision int64
				var visibility string
				e = tx.QueryRow(`SELECT c.revision,COALESCE(a.visibility,'private') FROM api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id WHERE c.id=? AND c.application_id=? AND c.deleted=0 AND (c.owner=? OR a.visibility='team')`, c.ID, sv.ApplicationID, actor(r)).Scan(&revision, &visibility)
				if e != nil || revision != expected[strconv.FormatInt(c.ID, 10)] || v.Visibility == "team" && visibility != "team" {
					fail(w, 409, "测试集中的用例或权限已变化，请重新预览")
					return
				}
			}
		}
		var res sql.Result
		if id == 0 {
			res, e = tx.Exec(`INSERT INTO api_test_suites(application_id,owner,visibility,document,revision,deleted,updated_at) VALUES(?,?,?,?,?,0,?)`, sv.ApplicationID, v.Owner, v.Visibility, enc, v.Revision, time.Now().Unix())
			if e == nil {
				v.ID, _ = res.LastInsertId()
			}
		} else {
			res, e = tx.Exec(`UPDATE api_test_suites SET visibility=?,document=?,revision=?,updated_at=? WHERE id=? AND application_id=? AND owner=? AND revision=? AND deleted=0`, v.Visibility, enc, v.Revision, time.Now().Unix(), id, sv.ApplicationID, v.Owner, oldRevision)
			if e == nil {
				n, _ := res.RowsAffected()
				if n != 1 {
					e = fmt.Errorf("测试集已变化")
				}
			}
		}
		if e != nil {
			fail(w, 409, e)
			return
		}
		if e = tx.Commit(); e != nil {
			fail(w, 409, e)
			return
		}
		wj(w, v)
	}
	mux.HandleFunc("POST /api/api-testing/suites", save)
	mux.HandleFunc("POST /api/api-testing/mcp/suites", save)
	mux.HandleFunc("PUT /api/api-testing/mcp/suites/{id}", save)
	mux.HandleFunc("PUT /api/api-testing/suites/{id}", save)
	mux.HandleFunc("DELETE /api/api-testing/suites/{id}", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v, e := st.apiTestSuite(sv.ApplicationID, actor(r), id, admin(r))
		if e != nil || !v.CanEdit {
			fail(w, 403, "无权删除测试集")
			return
		}
		if _, e = st.db.Exec(`UPDATE api_test_suites SET deleted=1 WHERE id=? AND application_id=? AND revision=?`, id, sv.ApplicationID, v.Revision); e != nil {
			fail(w, 500, e)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
}

var errAPITestSuiteConflict = errors.New("测试集修订冲突")

func apiTestSuiteErrorStatus(err error) int {
	if errors.Is(err, errAPITestSuiteConflict) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// Shared by browser save and MCP preview; previews never persist or send HTTP.
func (s *Store) validateAPITestSuite(v *apiTestSuite, app, owner string, isAdmin bool) ([]*apiTestCase, error) {
	if v.Visibility == "" {
		v.Visibility = "private"
	}
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 200 || len(v.CaseIDs) == 0 || len(v.CaseIDs) > 20 || (v.Visibility != "private" && v.Visibility != "team") {
		return nil, fmt.Errorf("请填写测试集名称，选择 1–20 个用例和有效共享范围")
	}
	v.Owner = owner
	if v.ID > 0 {
		old, e := s.apiTestSuite(app, owner, v.ID, isAdmin)
		if e != nil || !old.CanEdit || old.Revision != v.Revision {
			return nil, fmt.Errorf("%w: 测试集已变化或无权修改", errAPITestSuiteConflict)
		}
		v.Owner = old.Owner
	} else if v.Revision != 0 {
		return nil, fmt.Errorf("%w: 新测试集修订号无效", errAPITestSuiteConflict)
	}
	seen := map[int64]bool{}
	var bindingCases []*apiTestCase
	for _, caseID := range v.CaseIDs {
		c, e := s.apiTestCase(app, owner, caseID)
		if e != nil || seen[caseID] {
			return nil, fmt.Errorf("包含不可访问或重复的用例")
		}
		if v.Visibility == "team" && c.Visibility != "team" {
			return nil, fmt.Errorf("共享测试集中的用例也需要设为团队可用")
		}
		seen[caseID] = true
		bindingCases = append(bindingCases, c)
	}
	if e := validateAPITestBindings(v.Bindings, bindingCases); e != nil {
		return nil, e
	}
	return bindingCases, nil
}
