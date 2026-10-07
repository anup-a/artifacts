package api

import "log/slog"

// funnel logs a step of getting started that the landing page cannot see:
// a guest key made, a workspace's first page, an agent connected with a
// code. GA counts the prompt being copied; these lines in the server log
// say how many of those copies became real use. One line per step, with
// opaque ids only.
func funnel(step string, attrs ...any) {
	slog.Info("funnel", append([]any{"step", step}, attrs...)...)
}
