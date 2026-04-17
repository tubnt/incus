package portal

import (
	"net/http/httptest"
	"testing"
)

func TestParsePageParams(t *testing.T) {
	cases := []struct {
		url        string
		wantLimit  int
		wantOffset int
	}{
		{"/x", 50, 0},
		{"/x?limit=20&offset=10", 20, 10},
		{"/x?limit=0", 50, 0},
		{"/x?limit=-5", 50, 0},
		{"/x?limit=9999", 200, 0},
		{"/x?offset=-10", 50, 0},
		{"/x?limit=abc&offset=xyz", 50, 0},
		{"/x?limit=100&offset=500", 100, 500},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.url, nil)
		p := ParsePageParams(r)
		if p.Limit != c.wantLimit || p.Offset != c.wantOffset {
			t.Errorf("ParsePageParams(%s) = {Limit:%d Offset:%d}; want {%d %d}",
				c.url, p.Limit, p.Offset, c.wantLimit, c.wantOffset)
		}
	}
}
