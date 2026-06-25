package gateway

import "testing"

func TestHostnameToRegex(t *testing.T) {
	cases := []struct {
		in          string
		wantRegex   string
		wantLiteral bool
	}{
		{"foo.example.com", "^foo\\.example\\.com$", true},
		{"*.example.com", "^[^.]+\\.example\\.com$", false},
		{"", "", true},
	}
	for _, tc := range cases {
		gotRegex, isLiteral := HostnameToRegex(tc.in)
		if gotRegex != tc.wantRegex || isLiteral != tc.wantLiteral {
			t.Errorf("HostnameToRegex(%q) = (%q, %v); want (%q, %v)",
				tc.in, gotRegex, isLiteral, tc.wantRegex, tc.wantLiteral)
		}
	}
}

func TestHostnameMatches(t *testing.T) {
	cases := []struct {
		listener, route string
		want            bool
	}{
		{"", "foo.example.com", true},
		{"foo.example.com", "foo.example.com", true},
		{"foo.example.com", "bar.example.com", false},
		{"*.example.com", "foo.example.com", true},
		{"*.example.com", "foo.bar.example.com", false},
		{"*.example.com", "example.com", false},
	}
	for _, tc := range cases {
		if got := HostnameMatches(tc.listener, tc.route); got != tc.want {
			t.Errorf("HostnameMatches(%q,%q) = %v; want %v",
				tc.listener, tc.route, got, tc.want)
		}
	}
}
