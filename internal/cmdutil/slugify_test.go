package cmdutil

import "testing"

func TestProjectSlugify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain alnum", "myproject", "myproject"},
		{"mixed case", "MyProject", "myproject"},
		{"single space", "My App", "my-app"},
		{"multi space collapse", "My  App   Two", "my-app-two"},
		{"tab", "proj\twith\ttabs", "proj-with-tabs"},
		{"newline", "line\nbreak", "line-break"},
		{"leading whitespace", "  leading", "leading"},
		{"trailing whitespace", "trailing  ", "trailing"},
		{"both ends whitespace", "  both  ", "both"},
		{"dots preserved", "foo.bar", "foo.bar"},
		{"underscore preserved", "foo_bar", "foo_bar"},
		{"hyphen preserved", "foo-bar", "foo-bar"},
		{"dots and spaces", "foo.bar baz", "foo.bar-baz"},
		{"unicode passes through", "项目", "项目"},
		{"unicode with space", "项 目", "项-目"},
		{"control chars stripped", "proj\x00\x01\x02name", "projname"},
		{"DEL stripped", "proj\x7fname", "projname"},
		{"all control chars", "\x00\x01\x02\x03", ""},
		{"only whitespace trims to empty", "   ", ""},
		{"emoji passes", "proj🚀", "proj🚀"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ProjectSlugify(tc.in)
			if got != tc.want {
				t.Errorf("ProjectSlugify(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
