package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"math/big"
	"reflect"
	"strings"
	"time"
)

type apiTestEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Branch string `json:"branch,omitempty"`
}
type apiTestGraph struct {
	Entry       string        `json:"entry"`
	Concurrency int           `json:"concurrency"`
	Edges       []apiTestEdge `json:"edges"`
}
type apiTestCondition struct {
	Variable string      `json:"variable"`
	Operator string      `json:"operator"`
	Expected apiTestCell `json:"expected"`
}
type apiTestGraphPlan struct {
	guaranteed         map[string]bool
	order              []int
	incoming, outgoing [][]apiTestEdge
	index              map[string]int
}

func compatibleTestDefinition(old, input int, graph *apiTestGraph) bool {
	return old == 0 || old == input || old == 1 && input == 2 && graph != nil
}

func testNodeKind(s apiTestStep) string {
	if s.Kind == "" {
		if s.Request.Mode == "dubbo" {
			return "dubbo"
		}
		return "http"
	}
	return s.Kind
}
func conditionKey(c apiTestCondition) (string, error) {
	parts := strings.Split(c.Variable, ".")
	if len(parts) != 2 || !apiTestColumnRE.MatchString(parts[1]) {
		return "", fmt.Errorf("条件变量请使用 step.NAME、data.NAME 或 case.NAME")
	}
	switch parts[0] {
	case "step":
		return parts[1], nil
	case "data", "case":
		return c.Variable, nil
	}
	return "", fmt.Errorf("条件变量命名空间无效")
}
func conditionExpected(c apiTestCondition) (any, error) {
	if c.Operator == "exists" {
		return nil, nil
	}
	m, err := apiTestDataScope(apiTestDataRow{Name: "condition", Values: map[string]apiTestCell{"expected": c.Expected}})
	return m["data.expected"], err
}
func compareCondition(c apiTestCondition, scope map[string]any) (bool, error) {
	key, err := conditionKey(c)
	if err != nil {
		return false, err
	}
	actual, exists := scope[key]
	if c.Operator == "exists" {
		return exists && actual != nil, nil
	}
	if !exists {
		return false, fmt.Errorf("条件变量 %s 不存在", c.Variable)
	}
	expected, err := conditionExpected(c)
	if err != nil {
		return false, err
	}
	a, aok := actual.(json.Number)
	b, bok := expected.(json.Number)
	equal := reflect.DeepEqual(actual, expected)
	var cmp int
	if aok && bok {
		x, ok := new(big.Rat).SetString(a.String())
		y, other := new(big.Rat).SetString(b.String())
		if !ok || !other {
			return false, fmt.Errorf("条件数值无效")
		}
		cmp = x.Cmp(y)
		equal = cmp == 0
	}
	switch c.Operator {
	case "equals":
		return equal, nil
	case "not_equals":
		return !equal, nil
	case "greater", "less":
		if !aok || !bok {
			return false, fmt.Errorf("大小比较要求两个数值")
		}
		if c.Operator == "greater" {
			return cmp > 0, nil
		}
		return cmp < 0, nil
	}
	return false, fmt.Errorf("条件操作符无效")
}

func planAPITestGraph(c *apiTestCase) (*apiTestGraphPlan, error) {
	g := c.Graph
	if g == nil {
		return nil, fmt.Errorf("缺少图定义")
	}
	if g.Concurrency < 1 || g.Concurrency > 4 || len(g.Edges) > 30 {
		return nil, fmt.Errorf("图并发须为1–4，连线最多30条")
	}
	p := &apiTestGraphPlan{index: map[string]int{}, incoming: make([][]apiTestEdge, len(c.Steps)), outgoing: make([][]apiTestEdge, len(c.Steps))}
	for i, s := range c.Steps {
		p.index[s.ID] = i
	}
	entry, ok := p.index[g.Entry]
	if !ok {
		return nil, fmt.Errorf("图入口不存在")
	}
	seen := map[string]bool{}
	for _, e := range g.Edges {
		a, aok := p.index[e.From]
		b, bok := p.index[e.To]
		key := e.From + "/" + e.To
		if !aok || !bok || a == b || seen[key] {
			return nil, fmt.Errorf("连线端点无效、重复或自连接")
		}
		seen[key] = true
		p.outgoing[a] = append(p.outgoing[a], e)
		p.incoming[b] = append(p.incoming[b], e)
	}
	degrees := make([]int, len(c.Steps))
	queue := []int{}
	ends := 0
	conditions := []int{}
	for i, s := range c.Steps {
		in, out := len(p.incoming[i]), len(p.outgoing[i])
		degrees[i] = in
		if in == 0 {
			if i != entry {
				return nil, fmt.Errorf("存在未连接节点 %s", s.ID)
			}
			queue = append(queue, i)
		}
		if out == 0 {
			ends++
		}
		if testNodeKind(s) != "join" && in > 1 {
			return nil, fmt.Errorf("多条路径必须通过汇合节点连接")
		}
		switch testNodeKind(s) {
		case "http", "dubbo":
			if out > 1 {
				return nil, fmt.Errorf("接口节点仅允许一个后继，请添加条件或并行节点")
			}
		case "condition":
			if s.Condition == nil || out != 2 {
				return nil, fmt.Errorf("条件节点需要条件及真/假两条路径")
			}
			branches := map[string]bool{}
			for _, e := range p.outgoing[i] {
				branches[e.Branch] = true
			}
			if !branches["true"] || !branches["false"] {
				return nil, fmt.Errorf("条件连线须分别标记 true/false")
			}
			if _, err := conditionKey(*s.Condition); err != nil {
				return nil, err
			}
			switch s.Condition.Operator {
			case "equals", "not_equals", "greater", "less", "exists":
			default:
				return nil, fmt.Errorf("条件操作符无效")
			}
			if _, err := conditionExpected(*s.Condition); err != nil {
				return nil, err
			}
			if (s.Condition.Operator == "greater" || s.Condition.Operator == "less") && s.Condition.Expected.Type != "number" {
				return nil, fmt.Errorf("大小比较期望值必须为数字")
			}
			conditions = append(conditions, i)
		case "parallel":
			if out < 2 || out > 4 {
				return nil, fmt.Errorf("并行开始需要2–4条路径")
			}
		case "join":
			if in < 2 || out > 1 {
				return nil, fmt.Errorf("汇合需要至少两条输入及最多一个后继")
			}
		default:
			return nil, fmt.Errorf("节点类型无效")
		}
		if testNodeKind(s) != "condition" {
			for _, e := range p.outgoing[i] {
				if e.Branch != "" {
					return nil, fmt.Errorf("只有条件连线可以指定分支")
				}
			}
		}
	}
	if len(queue) != 1 || ends != 1 {
		return nil, fmt.Errorf("图需要唯一入口和终点，分支须通过汇合节点收敛")
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		p.order = append(p.order, i)
		for _, e := range p.outgoing[i] {
			j := p.index[e.To]
			degrees[j]--
			if degrees[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if len(p.order) != len(c.Steps) {
		return nil, fmt.Errorf("图包含循环")
	}
	// At most ten nodes: exhaustively check every possible condition route. A
	// reference must be defined on every active route, including after a join.
	conditionBit := map[int]int{}
	for bit, i := range conditions {
		conditionBit[i] = bit
	}
	for mask := 0; mask < 1<<len(conditions); mask++ {
		active := make([]bool, len(c.Steps))
		exports := make([]map[string]bool, len(c.Steps))
		for _, i := range p.order {
			available := map[string]bool{}
			active[i] = i == entry
			for _, e := range p.incoming[i] {
				j := p.index[e.From]
				enabled := active[j]
				if testNodeKind(c.Steps[j]) == "condition" {
					enabled = enabled && (e.Branch == "true") == ((mask&(1<<conditionBit[j])) != 0)
				}
				if enabled {
					active[i] = true
					for name := range exports[j] {
						available[name] = true
					}
				}
			}
			exports[i] = available
			if !active[i] {
				continue
			}
			s := c.Steps[i]
			if isTestRequest(s) {
				if err := apitest.ValidateStepReferences(s.Request, available); err != nil {
					return nil, fmt.Errorf("节点 %s：%w", s.Name, err)
				}
			}
			if s.Condition != nil {
				key, _ := conditionKey(*s.Condition)
				if strings.HasPrefix(s.Condition.Variable, "step.") && !available[key] {
					return nil, fmt.Errorf("条件节点 %s 引用了非必达上游变量", s.Name)
				}
			}
			for _, x := range s.Extractions {
				available[x.Name] = true
			}
		}
		for _, i := range p.order {
			if len(p.outgoing[i]) == 0 {
				if mask == 0 {
					p.guaranteed = map[string]bool{}
					for name := range exports[i] {
						p.guaranteed[name] = true
					}
				} else {
					for name := range p.guaranteed {
						if !exports[i][name] {
							delete(p.guaranteed, name)
						}
					}
				}
			}
		}
	}
	return p, nil
}

func graphOfCase(c apiTestCase) apiTestCase {
	if c.Graph != nil {
		return c
	}
	c.Graph = &apiTestGraph{Entry: c.Steps[0].ID, Concurrency: 1}
	for i := 1; i < len(c.Steps); i++ {
		c.Graph.Edges = append(c.Graph.Edges, apiTestEdge{From: c.Steps[i-1].ID, To: c.Steps[i].ID})
	}
	return c
}
func copyTestScope(values map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range values {
		out[k] = v
	}
	return out
}
func graphStatus(a, b string) string {
	rank := map[string]int{"success": 0, "canceled": 1, "failed": 2, "unknown": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

type apiGraphUpdate struct {
	index      int
	done       bool
	result     apitest.Result
	extraction apitest.ExtractionOutcome
}

// Only this coordinator mutates/persists the report. Workers own independent
// inputs and publish immutable progress snapshots; raw outputs never persist.
func (m *apiTestManager) runGraphUnit(ctx context.Context, v *apiTestRun, c apiTestCase, offset int, target apiTestTarget, p apiTestProfile, variables map[string]string, initial map[string]any, scenarios ...map[string]apiScenarioRuntime) (string, map[string]any) {
	c = graphOfCase(c)
	plan, err := planAPITestGraph(&c)
	if err != nil {
		return "failed", nil
	}
	n := len(c.Steps)
	done, active := make([]bool, n), make([]bool, n)
	branches := make([]bool, n)
	outputs := make([]map[string]any, n)
	state := "success"
	remaining := n
	updates := make(chan apiGraphUpdate, 32)
	persist := func() {
		copy := *v
		copy.Status = "running"
		if m.persist(copy) != nil {
			state = "unknown"
		}
	}
	for remaining > 0 && state == "success" && ctx.Err() == nil {
		ready := []int{}
		for _, i := range plan.order {
			if done[i] {
				continue
			}
			parentsDone := true
			for _, e := range plan.incoming[i] {
				if !done[plan.index[e.From]] {
					parentsDone = false
				}
			}
			if parentsDone {
				ready = append(ready, i)
			}
		}
		if len(ready) == 0 {
			state = "failed"
			break
		}
		running := 0
		for _, i := range ready {
			if running >= c.Graph.Concurrency {
				break
			}
			s := c.Steps[i]
			scope := copyTestScope(initial)
			active[i] = s.ID == c.Graph.Entry
			for _, e := range plan.incoming[i] {
				j := plan.index[e.From]
				enabled := active[j] && (testNodeKind(c.Steps[j]) != "condition" || (e.Branch == "true") == branches[j])
				if enabled {
					active[i] = true
					for k, value := range outputs[j] {
						scope[k] = value
					}
				}
			}
			r := &v.Steps[offset+i]
			if !active[i] {
				r.Status = "skipped"
				r.SkipReason = "branch_not_selected"
				done[i] = true
				remaining--
				continue
			}
			if !isTestRequest(s) {
				r.Status = "success"
				if s.Condition != nil {
					value, e := compareCondition(*s.Condition, scope)
					if e != nil {
						r.Status = "failed"
						r.Result.Error = e.Error()
						state = "failed"
					} else {
						branches[i] = value
						r.BranchTaken = &value
					}
				}
				outputs[i] = scope
				done[i] = true
				remaining--
				if state != "success" {
					break
				}
				continue
			}
			r.Status = "running"
			running++
			go func(index int, step apiTestStep, input map[string]any) {
				stepTarget, stepAuth, stepVariables := target, p.Auth, variables
				if len(scenarios) > 0 {
					runtime, ok := scenarioRuntimeFor(scenarios[0], step)
					if !ok {
						updates <- apiGraphUpdate{index: index, done: true, result: apitest.Result{Error: "节点的冻结应用目标不存在"}}
						return
					}
					stepTarget, stepAuth, stepVariables = runtime.Target, runtime.Auth, runtime.Variables
				}
				stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.Request.Timeout)*time.Second)
				defer cancel()
				progress := apitest.Result{}
				send := func() {
					raw, _ := json.Marshal(progress)
					var snapshot apitest.Result
					_ = json.Unmarshal(raw, &snapshot)
					if len(scenarios) > 0 {
						snapshot = redactScenarioResult(snapshot, scenarios[0][step.Service].Redactions)
					}
					updates <- apiGraphUpdate{index: index, result: snapshot}
				}
				result, extraction := apitest.PerformScoped(stepCtx, m.client, stepTarget.BaseURL, step.Request, stepAuth, stepVariables, input, step.Extractions, func(event apitest.Event) { progress.Events = append(progress.Events, event); send() }, func(metadata apitest.Result) { progress = metadata; send() })
				if len(scenarios) > 0 {
					result = redactScenarioResult(result, scenarios[0][step.Service].Redactions)
				}
				updates <- apiGraphUpdate{index: index, done: true, result: result, extraction: extraction}
			}(i, s, scope)
			outputs[i] = scope
		}
		persist()
		for running > 0 {
			u := <-updates
			r := &v.Steps[offset+u.index]
			r.Result = u.result
			if u.done {
				running--
				done[u.index] = true
				remaining--
				r.Status = apiTestResultStatus(u.result)
				r.Extractions = u.extraction.Checks
				v.DurationMS += u.result.DurationMS
				state = graphStatus(state, r.Status)
				if r.Status == "success" {
					for k, value := range u.extraction.Values {
						outputs[u.index][k] = value
					}
				}
			}
			v.Result = u.result
			v.Request = r.Request
			v.Method = r.Request.Method
			v.Mode = r.Request.Mode
			v.Path = r.Request.Path
			persist()
		}
	}
	if ctx.Err() != nil {
		state = graphStatus(state, "canceled")
	}
	for i := range c.Steps {
		if !done[i] {
			v.Steps[offset+i].Status = "skipped"
			v.Steps[offset+i].SkipReason = "execution_stopped"
		}
	}
	merged := map[string]any{}
	if state == "success" {
		for _, i := range plan.order {
			if len(plan.outgoing[i]) == 0 {
				merged = outputs[i]
			}
		}
	}
	return state, merged
}

func (m *apiTestManager) runGraphCases(ctx context.Context, v *apiTestRun, planned []apiTestPlannedUnit, target apiTestTarget, p apiTestProfile, variables map[string]string, bindings []apiTestCaseBinding, scenarios ...map[string]apiScenarioRuntime) {
	caseValues := map[string]any{}
	v.Status = "success"
	for i, unit := range planned {
		initial := copyTestScope(unit.Data)
		for _, b := range bindings {
			if b.TargetCaseID == unit.Case.ID {
				if value, ok := caseValues[b.key()]; ok {
					initial["case."+b.Name] = value
				}
			}
		}
		v.Units[i].Status = "running"
		state, outputs := m.runGraphUnit(ctx, v, unit.Case, v.Units[i].Start, target, p, variables, initial, scenarios...)
		v.Units[i].Status = state
		v.Status = state
		if state != "success" {
			break
		}
		for _, b := range bindings {
			if b.SourceCaseID == unit.Case.ID && (b.SourceRow == "" || b.SourceRow == unit.RowName) {
				if value, ok := outputs[b.SourceName]; ok {
					caseValues[b.key()] = value
				}
			}
		}
	}
	for i := range v.Steps {
		if v.Steps[i].Status == "pending" {
			v.Steps[i].Status = "skipped"
			v.Steps[i].SkipReason = "execution_stopped"
		}
	}
	for i := range v.Units {
		if v.Units[i].Status == "pending" {
			v.Units[i].Status = "skipped"
		}
	}
	v.EndedAt = time.Now().Unix()
	_ = m.persist(*v)
}
