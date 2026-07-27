package supabase

import (
	"fmt"
	"strings"
)

type urlBuilder struct {
	base   string
	args   []string
	parks  []string
}

func newURLBuilder(base string) *urlBuilder {
	return &urlBuilder{base: base}
}

func (b *urlBuilder) Opt(key, val string) {
	if key == "order" {
		b.parks = append(b.parks, val)
		return
	}
	if val == "" {
		return
	}
	b.args = append(b.args, fmt.Sprintf("%s=%s", key, val))
}

func (b *urlBuilder) Build() string {
	parts := append([]string{}, b.args...)
	if len(b.parks) > 0 {
		parts = append(parts, "order="+strings.Join(b.parks, ","))
	}
	base := b.base
	if strings.Contains(base, "?") {
		base = base + "&" + strings.Join(parts, "&")
	} else {
		base = base + "?" + strings.Join(parts, "&")
	}
	return base
}