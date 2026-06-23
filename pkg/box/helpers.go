package box

import (
	"net/url"
	"strconv"
)

// markerPage holds the marker-pagination fields returned by Box list endpoints.
// Box rejects offset values above 10,000; marker-based pagination has no such limit.
type markerPage struct {
	NextMarker string `json:"next_marker"`
}

// markerQuery builds query params for marker-based pagination (usemarker=true).
func markerQuery(marker string) url.Values {
	q := url.Values{}
	q.Set("usemarker", "true")
	q.Set("limit", strconv.Itoa(defaultLimit))
	if marker != "" {
		q.Set("marker", marker)
	}
	return q
}
