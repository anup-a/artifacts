package cover

import (
	"strings"
	"testing"
	"time"
)

// A wash without a project would be a grey gradient, so it becomes dots.
func TestWashWithoutProjectFallsBackToDots(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	noProject := string(Render(Card{ID: "x", Title: "t", Host: "byagent.dev", PublishedAt: at, Style: "wash"}))
	if strings.Contains(noProject, `id="w"`) || !strings.Contains(noProject, `id="p"`) {
		t.Fatalf("no-project wash should render the dot pattern, got %s", noProject)
	}
	withProject := string(Render(Card{ID: "x", Title: "t", Project: "finance", Host: "byagent.dev", PublishedAt: at, Style: "wash"}))
	if !strings.Contains(withProject, `id="w"`) {
		t.Fatalf("wash with a project should keep the gradient, got %s", withProject)
	}
}
