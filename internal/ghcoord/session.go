package ghcoord

import (
	"sort"
	"strings"
)

// SessionMember records a GitHub account that opted into one PR's collaboration
// discussion. The account login is GitHub-supplied; Label is self-reported and
// grants no broker identity, authorization, path lease, or merge permission.
type SessionMember struct {
	Login       string      `json:"login"`
	Label       string      `json:"label"`
	Attribution Attribution `json:"attribution"`
	CommentID   int64       `json:"comment_id"`
}

// validSessionLabel restricts untrusted display-only labels to short ASCII
// strings. Labels must not contain Markdown syntax or control characters.
func validSessionLabel(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		if i > 0 && strings.ContainsRune(" ._+/-()", r) {
			continue
		}
		return false
	}
	return true
}

// ParseSessionMembers folds join/leave declarations from PR issue comments.
// Membership is display-only, and the GitHub account (not the claimed AI
// provider) is its sole authenticated attribute. Sorting by numeric comment
// ID makes the latest comment decisive and allows a later rejoin after leave.
func ParseSessionMembers(reg *Registry, comments []ghComment) []SessionMember {
	sorted := append([]ghComment(nil), comments...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	active := make(map[string]SessionMember)
	for _, cm := range sorted {
		login := strings.TrimSpace(cm.User.Login)
		if login == "" || strings.EqualFold(login, "github-actions[bot]") || strings.HasPrefix(cm.Body, CommentMarker) {
			continue
		}
		key := strings.ToLower(login)
		for _, raw := range strings.Split(cm.Body, "\n") {
			line := strings.TrimSpace(raw)
			if strings.EqualFold(line, "Collaborate-Leave") || strings.EqualFold(line, "Collaborate-Leave:") {
				delete(active, key)
				continue
			}
			prefix, value, ok := strings.Cut(line, ":")
			if !ok || !strings.EqualFold(prefix, "Collaborate-Join") {
				continue
			}
			label := strings.TrimSpace(value)
			if !validSessionLabel(label) {
				continue
			}
			active[key] = SessionMember{
				Login: login, Label: label,
				Attribution: reg.AttributeReviewer(login), CommentID: cm.ID,
			}
		}
	}
	members := make([]SessionMember, 0, len(active))
	for _, member := range active {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return strings.ToLower(members[i].Login) < strings.ToLower(members[j].Login) })
	return members
}
