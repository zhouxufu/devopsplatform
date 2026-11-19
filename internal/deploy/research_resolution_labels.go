package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type researchResolutionDictionary struct {
	labels  map[string]string
	expires time.Time
}

func copyResolutionLabels(values ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, labels := range values {
		for code, label := range labels {
			if code != "" && label != "" {
				out[code] = label
			}
		}
	}
	return out
}

// The native resolve form exposes the source's custom enum names. Reading its
// GET endpoint does not resolve the Bug. Cache only labels, not the form/Bug,
// and never share them across authenticated sessions or product contexts.
func (z *zentaoLegacy) researchResolutionLabels(ctx context.Context, product, bugID string, native map[string]string) map[string]string {
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	if z.resolutionDictionaries == nil || len(z.resolutionDictionaries) >= 64 {
		z.resolutionDictionaries = map[string]researchResolutionDictionary{}
	}
	if len(native) > 0 {
		z.resolutionDictionaries[product] = researchResolutionDictionary{copyResolutionLabels(native), time.Now().Add(5 * time.Minute)}
	}
	if cached, ok := z.resolutionDictionaries[product]; ok && time.Now().Before(cached.expires) {
		return copyResolutionLabels(cached.labels)
	}
	if ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	remote := &ResearchRemote{}
	// This GET reads form options only; it neither compiles nor changes the
	// native search query. Preserve list search setup when changing pages.
	err := z.call(ctx, "bug", "resolve", url.Values{"bugID": {bugID}}, nil, remote)
	labels := map[string]string{}
	// Reject redirected forms or data for another Bug/product.
	if err == nil && researchValue(remote.Bug, "id") == bugID && researchValue(remote.Bug, "product") == product {
		labels = copyResolutionLabels(remote.Resolutions, remote.ResolutionList)
		if len(labels) == 0 {
			// Stock 16.0 exposes lang.bug.resolutionList in its HTML select,
			// not in the JSON controller view data. Parse that exact form;
			// never execute scripts, load assets, or submit the form.
			if values, readErr := z.researchResolutionHTML(ctx, bugID); readErr == nil {
				labels = values
			}
		}
	}
	ttl := 5 * time.Minute
	if len(labels) == 0 {
		// A denied/unavailable dictionary must not fail the list or cause N+1
		// requests on every refresh. Retry soon rather than caching a failure.
		ttl = 30 * time.Second
	}
	z.resolutionDictionaries[product] = researchResolutionDictionary{labels, time.Now().Add(ttl)}
	return copyResolutionLabels(labels)
}

// Caller holds queryMu and has verified this Bug/product through JSON.
func (z *zentaoLegacy) researchResolutionHTML(ctx context.Context, bugID string) (map[string]string, error) {
	address, err := z.requestURL("bug", "resolve", url.Values{"bugID": {bugID}})
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if z.pathInfo {
		u.Path = strings.TrimSuffix(u.Path, ".json") + ".html"
	} else {
		q := u.Query()
		q.Set("t", "html")
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := z.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法读取来源方案字典")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode >= 300 && resp.StatusCode < 400 && zentaoLoginRedirect(resp.Header.Get("Location"))) {
		return nil, z.expireAuthentication()
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("来源方案字典不可读取")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, fmt.Errorf("来源方案字典响应过大")
	}
	return parseResearchResolutionHTML(string(raw))
}

func parseResearchResolutionHTML(raw string) (map[string]string, error) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("来源方案字典无法解析")
	}
	attr := func(n *html.Node, name string) string {
		for _, a := range n.Attr {
			if a.Key == name {
				return a.Val
			}
		}
		return ""
	}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return ""
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(text(c))
		}
		return b.String()
	}
	labels, selects := map[string]string{}, 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "select" && attr(n, "name") == "resolution" {
			selects++
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				var options func(*html.Node)
				options = func(o *html.Node) {
					if o.Type == html.ElementNode && o.Data == "option" {
						code := attr(o, "value")
						label := strings.Join(strings.Fields(text(o)), " ")
						if code != "" && len(code) <= 64 && !strings.ContainsAny(code, "\x00\r\n") && label != "" && len(label) <= 256 {
							labels[code] = label
						}
						return
					}
					for child := o.FirstChild; child != nil; child = child.NextSibling {
						options(child)
					}
				}
				options(c)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if selects != 1 || len(labels) == 0 || len(labels) > 100 {
		return nil, fmt.Errorf("来源页面未提供有效的方案选项")
	}
	return labels, nil
}

func (z *zentaoLegacy) enrichResearchResolution(ctx context.Context, c *ResourceConnection, remote *ResearchRemote) {
	labels := z.researchResolutionLabels(ctx, researchValue(remote.Bug, "product"), researchValue(remote.Bug, "id"), copyResolutionLabels(remote.Resolutions, remote.ResolutionList))
	remote.Resolutions = labels
	applyResolutionOptions(c, remote)
}

func (z *zentaoLegacy) enrichLiveResolutionLabels(ctx context.Context, c *ResourceConnection, out *researchLivePage) {
	out.FieldLabels = map[string]map[string]string{"resolution": copyResolutionLabels(sourceLabels(c, "resolution"))}
	byProduct := map[string]map[string]string{}
	// Bound the entire optional enrichment, including all-product pages.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for i := range out.Items {
		item := &out.Items[i]
		product := strconv.Itoa(item.ProductID)
		if !liveProductAllowed(c, item.ProductID) {
			continue
		}
		labels, seen := byProduct[product]
		if !seen {
			labels = copyResolutionLabels(z.researchResolutionLabels(ctx, product, item.ExternalID, nil), sourceLabels(c, "resolution"))
			byProduct[product] = labels
			for code, label := range labels {
				out.FieldLabels["resolution"][code] = label
			}
		}
		if label := labels[item.Resolution]; label != "" {
			item.ResolutionLabel = label
		}
	}
}
