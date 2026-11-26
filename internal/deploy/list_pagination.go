package deploy

import (
	"fmt"
	"net/http"
	"strconv"
)

// The UI offers 20/50/100. Preserve existing explicit small API pages while
// keeping the same bound for every newly pageable ordinary list.
func requestListPageSize(r *http.Request) (int, error) {
	if !r.URL.Query().Has("page_size") {
		return 20, nil
	}
	size, err := strconv.Atoi(r.URL.Query().Get("page_size"))
	if err != nil || size < 1 || size > 100 {
		return 0, fmt.Errorf("每页需要 1–100 条")
	}
	return size, nil
}
