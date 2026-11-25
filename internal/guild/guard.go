package guild

// Guard stops one human message from turning into an unbounded model loop.
type Guard struct {
	// MaxTurns is how many character replies one human message may cause.
	MaxTurns int
	// MaxTokens is a rough cap on prompt + reply size for that same burst.
	MaxTokens int
}

func (g Guard) normalize() Guard {
	if g.MaxTurns <= 0 {
		g.MaxTurns = 3
	}
	if g.MaxTokens <= 0 {
		g.MaxTokens = 8000
	}
	return g
}

// Admit reports whether id may speak in this burst. A character who already
// spoke, or who is the same id twice, is refused. That is the loop guard.
func Admit(seen map[string]bool, id string) bool {
	if id == "" || seen[id] {
		return false
	}
	seen[id] = true
	return true
}

// AllowSpend reports whether another model call still fits the token cap.
func (g Guard) AllowSpend(spent int) bool {
	g = g.normalize()
	return spent < g.MaxTokens
}

// AllowTurn reports whether another reply still fits the turn cap.
func (g Guard) AllowTurn(turns int) bool {
	g = g.normalize()
	return turns < g.MaxTurns
}

func estimateTokens(parts ...string) int {
	n := 0
	for _, p := range parts {
		n += len(p)/4 + 1
	}
	return n
}
