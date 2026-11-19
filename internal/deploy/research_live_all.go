package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type liveSourcePosition struct {
	Page   int    `json:"page"`
	Offset int    `json:"offset"`
	Cursor string `json:"cursor,omitempty"`
	Done   bool   `json:"done,omitempty"`
}
type allLiveCursor struct {
	Digest  string                        `json:"digest"`
	Page    int                           `json:"page"`
	Sources map[string]liveSourcePosition `json:"sources"`
}

// Merge bounded source pages. The cursor contains only positions, never source
// records or a claimed identity. Each read rechecks the existing ACL/product
// rules and projects current fields over stable platform delivery references.
func (s *Store) researchAllLivePage(ctx context.Context, actor string, f researchLiveFilter) (researchLivePage, error) {
	out := researchLivePage{Items: []researchLiveItem{}, Products: map[string]string{}, People: map[string]string{}, Page: f.Page, PageSize: f.PageSize, CheckedAt: time.Now().Unix(), ReadIdentity: "sources", QueryMode: "live_sources"}
	if f.Product != 0 && f.Connection == "" {
		return out, fmt.Errorf("请先选择来源，再筛选产品")
	}
	names := []string{}
	if f.Connection == "" {
		names = append(names, "manual")
	}
	configurations := []any{}
	for _, c := range s.Connections() {
		if c.Kind == "zentao" && (f.Connection == "" || c.Name == f.Connection) {
			if f.Kind != "requirement" {
				names = append(names, c.Name)
			}
			if f.Kind == "" || f.Kind == "requirement" {
				products, e := zentaoProductIDs(&c)
				if e != nil {
					return out, e
				}
				if f.Product > 0 {
					if !liveProductAllowed(&c, f.Product) {
						return out, fmt.Errorf("产品不在允许范围")
					}
					products = []int{f.Product}
				}
				for _, product := range products {
					names = append(names, c.Name+"::requirements:"+strconv.Itoa(product))
				}
			}
			configurations = append(configurations, []any{c.Name, c.Endpoint, c.Username, c.Config})
		}
	}
	if len(configurations) > 12 || len(names) > 32 {
		return out, fmt.Errorf("来源或产品过多，请先选择一个来源及产品查询")
	}
	if len(names) == 0 {
		return out, fmt.Errorf("来源连接不存在")
	}
	sort.Strings(names)
	filter := f
	filter.Page = 0
	filter.Cursor = ""
	digest := digestJSON([]any{actor, filter, configurations, names})
	state := allLiveCursor{Digest: digest, Page: f.Page, Sources: map[string]liveSourcePosition{}}
	for _, name := range names {
		state.Sources[name] = liveSourcePosition{Page: 1}
	}
	if f.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		if e != nil || len(raw) > 16384 || json.Unmarshal(raw, &state) != nil || state.Digest != digest || state.Page != f.Page || len(state.Sources) != len(names) {
			return out, fmt.Errorf("查询条件或来源已变化，请重新查询第一页")
		}
		for _, name := range names {
			p, ok := state.Sources[name]
			if !ok || p.Page < 1 || p.Page > 1000000 || p.Offset < 0 || p.Offset > f.PageSize || len(p.Cursor) > 4096 {
				return out, fmt.Errorf("来源游标无效，请重新查询第一页")
			}
		}
	} else if f.Page != 1 {
		return out, fmt.Errorf("全部来源查询需使用下一页游标")
	}
	pages := map[string]researchLivePage{}
	conflicts := map[string]bool{}
	reads := 0
	load := func(name string) error {
		p := state.Sources[name]
		q := f
		q.Connection = name
		if connection, product, ok := strings.Cut(name, "::requirements:"); ok {
			q.Connection = connection
			q.Product, _ = strconv.Atoi(product)
			q.Kind = "requirement"
		} else if name != "manual" {
			q.Kind = "bug"
		}
		q.Page = p.Page
		q.Cursor = p.Cursor
		// A requirement filter has no upstream Bug equivalent. The local platform
		// is still read now, rather than being exposed as a separate query mode.
		v, err := s.ResearchLivePage(ctx, actor, q)
		if err != nil {
			return fmt.Errorf("%s：%w", map[bool]string{true: "本平台", false: name}[name == "manual"], err)
		}
		if v.Page != p.Page {
			return fmt.Errorf("来源内容已变化，请刷新第一页")
		}
		if p.Offset > len(v.Items) {
			return fmt.Errorf("来源内容已变化，请刷新第一页")
		}
		pages[name] = v
		reads++
		for key, label := range v.People {
			if old, ok := out.People[key]; ok && old != label {
				conflicts[key] = true
			}
			if !conflicts[key] {
				out.People[key] = label
			} else {
				delete(out.People, key)
			}
		}
		if f.Connection != "" || len(names) == 1 {
			for k, label := range v.Products {
				out.Products[k] = label
			}
		}
		for _, environment := range v.ReleaseEnvironments {
			if !containsString(out.ReleaseEnvironments, environment) {
				out.ReleaseEnvironments = append(out.ReleaseEnvironments, environment)
			}
		}
		return nil
	}
	for _, name := range names {
		if !state.Sources[name].Done {
			if err := load(name); err != nil {
				return out, err
			}
		}
	}
	for len(out.Items) < f.PageSize {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		// Refill before comparing heads; skipping an exhausted source would break
		// ordering and could silently omit its next record.
		budget := false
		for _, name := range names {
			for !state.Sources[name].Done {
				p := state.Sources[name]
				v := pages[name]
				if p.Offset < len(v.Items) {
					break
				}
				if !v.HasMore {
					p.Done = true
					state.Sources[name] = p
					break
				}
				p = liveSourcePosition{Page: v.Page + 1, Cursor: v.NextCursor}
				state.Sources[name] = p
				if reads >= len(names)+f.PageSize+4 {
					budget = true
					break
				}
				if err := load(name); err != nil {
					return out, err
				}
			}
			if budget {
				break
			}
		}
		if budget {
			break
		}
		chosen := ""
		for _, name := range names {
			p := state.Sources[name]
			if p.Done {
				continue
			}
			v := pages[name]
			if p.Offset >= len(v.Items) {
				continue
			}
			if chosen == "" || liveItemBefore(v.Items[p.Offset], pages[chosen].Items[state.Sources[chosen].Offset], f.Order) {
				chosen = name
			}
		}
		if chosen == "" {
			break
		}
		p := state.Sources[chosen]
		out.Items = append(out.Items, pages[chosen].Items[p.Offset])
		p.Offset++
		state.Sources[chosen] = p
	}
	for _, name := range names {
		p := state.Sources[name]
		if p.Done {
			continue
		}
		v := pages[name]
		if p.Offset == len(v.Items) {
			if v.HasMore {
				p = liveSourcePosition{Page: v.Page + 1, Cursor: v.NextCursor}
			} else {
				p.Done = true
			}
			state.Sources[name] = p
		}
		if !p.Done {
			out.HasMore = true
		}
	}
	out.Total = (f.Page-1)*f.PageSize + len(out.Items)
	if out.HasMore {
		out.Total++
		state.Page = f.Page + 1
		raw, _ := json.Marshal(state)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	// A live federation has no stable exact total; don't manufacture a count by
	// adding unscoped upstream candidate counts.
	out.TotalExact = false
	return out, nil
}
func liveItemBefore(a, b researchLiveItem, order string) bool {
	field, direction := "updated", "desc"
	if parts := strings.Split(order, "_"); len(parts) == 2 {
		field, direction = parts[0], parts[1]
	}
	value := func(v researchLiveItem) string {
		switch field {
		case "severity":
			return v.Severity
		case "priority":
			return v.Priority
		case "resolution":
			return v.Resolution
		}
		if v.SourceUpdatedAt != "" {
			return v.SourceUpdatedAt
		}
		return time.Unix(v.UpdatedAt, 0).Format("2006-01-02 15:04:05")
	}
	av, bv := value(a), value(b)
	if av == "" || bv == "" {
		if av != bv {
			return av != ""
		}
	}
	less, equal := av < bv, av == bv
	if field == "severity" || field == "priority" {
		an, ae := strconv.Atoi(av)
		bn, be := strconv.Atoi(bv)
		if ae == nil && be == nil {
			less, equal = an < bn, an == bn
		}
	}
	if !equal {
		if direction == "asc" {
			return less
		}
		return !less
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	ai, ae := strconv.ParseInt(a.ExternalID, 10, 64)
	bi, be := strconv.ParseInt(b.ExternalID, 10, 64)
	if ae == nil && be == nil && ai != bi {
		return ai > bi
	}
	return a.ID > b.ID
}
