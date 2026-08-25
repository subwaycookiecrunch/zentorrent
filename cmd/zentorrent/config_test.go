package main

import (
	"testing"

	"github.com/subwaycookiecrunch/zentorrent/internal/config"
)

func TestResolveTMDBKeyPrecedence(t *testing.T) {
	t.Setenv("TMDB_API_KEY", "")
	if got := config.ResolveTMDBKey(""); got != config.FallbackTMDBKey {
		t.Errorf("empty inputs should yield fallback, got %q", got)
	}
	if got := config.ResolveTMDBKey("cfgkey"); got != "cfgkey" {
		t.Errorf("config key must win over fallback, got %q", got)
	}
	t.Setenv("TMDB_API_KEY", "envkey")
	if got := config.ResolveTMDBKey(""); got != "envkey" {
		t.Errorf("env key expected, got %q", got)
	}
	if got := config.ResolveTMDBKey("cfgkey"); got != "cfgkey" {
		t.Errorf("config key must win over env, got %q", got)
	}
	if got := config.ResolveTMDBKey("   "); got != "envkey" {
		t.Errorf("whitespace config counts as empty, got %q", got)
	}
}
