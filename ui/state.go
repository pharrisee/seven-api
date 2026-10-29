package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// StateCookie holds the UI's disclosure state (which tag groups and sections
// are open). It is HttpOnly and only written by the server.
const StateCookie = "sdtd_ui"

// State is the server-side mirror of the viewer's disclosure state.
type State struct {
	Tags     map[string]bool `json:"tags,omitempty"`
	Sections map[string]bool `json:"sections,omitempty"`
}

// ReadState decodes the UI state cookie. Malformed cookies are ignored.
func ReadState(r *http.Request) State {
	state := State{
		Tags:     map[string]bool{},
		Sections: map[string]bool{},
	}
	cookie, err := r.Cookie(StateCookie)
	if err != nil {
		return state
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return state
	}
	var decoded State
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return state
	}
	if decoded.Tags != nil {
		state.Tags = decoded.Tags
	}
	if decoded.Sections != nil {
		state.Sections = decoded.Sections
	}
	return state
}

// WriteState persists the UI state cookie.
func WriteState(w http.ResponseWriter, r *http.Request, state State) {
	raw, err := json.Marshal(state)
	if err != nil {
		return
	}
	cookie := &http.Cookie{
		Name:     StateCookie,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   365 * 24 * 60 * 60,
	}
	if r.TLS != nil {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

// TagOpen reports whether a tag group is open, falling back to def when the
// user has never toggled it.
func (s State) TagOpen(name string, def bool) bool {
	if open, ok := s.Tags[name]; ok {
		return open
	}
	return def
}

// SectionOpen reports whether a section is open. Sections default to open.
func (s State) SectionOpen(name string) bool {
	if open, ok := s.Sections[name]; ok {
		return open
	}
	return true
}

// SetTag records a tag group's open state.
func (s *State) SetTag(name string, open bool) {
	if s.Tags == nil {
		s.Tags = map[string]bool{}
	}
	s.Tags[name] = open
}

// SetSection records a section's open state.
func (s *State) SetSection(name string, open bool) {
	if s.Sections == nil {
		s.Sections = map[string]bool{}
	}
	s.Sections[name] = open
}
