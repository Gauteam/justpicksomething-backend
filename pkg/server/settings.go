package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

const maxVotesPerMember = 100

type settingsBody struct {
	Name *string `json:"name"`
	// Raw so "maxItems": null (remove the limit) differs from leaving it out.
	MaxItems            json.RawMessage `json:"maxItems"`
	VoteMode            *string         `json:"voteMode"`
	VotesPerMember      *int            `json:"votesPerMember"`
	AllowDuplicateVotes *bool           `json:"allowDuplicateVotes"`
	Locked              *bool           `json:"locked"`
}

// handleUpdateSettings changes room settings and locks or unlocks the room.
// While locked, only unlocking is allowed.
func handleUpdateSettings(w http.ResponseWriter, r *http.Request, s db.Session) {
	var body settingsBody
	if !decodeBody(w, r, &body) {
		return
	}
	patch, err := settingsPatch(s.Room, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.Room.LockedAt != nil && changesMoreThanLock(patch) {
		writeError(w, http.StatusConflict, "unlock the room before changing settings")
		return
	}

	err = db.UpdateSettings(r.Context(), s.Room, patch)
	if errors.Is(err, db.ErrBelowCount) {
		writeError(w, http.StatusConflict, "there are already more options than that")
		return
	}
	if err != nil {
		internalError(w, "update settings", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func settingsPatch(room db.Room, b settingsBody) (db.SettingsPatch, error) {
	p := db.SettingsPatch{VoteMode: b.VoteMode, VotesPerMember: b.VotesPerMember, AllowDuplicateVotes: b.AllowDuplicateVotes, Locked: b.Locked}

	if b.Name != nil {
		name, err := cleanText("name", *b.Name, maxRoomNameLen)
		if err != nil {
			return p, err
		}
		p.Name = &name
	}
	if len(b.MaxItems) > 0 {
		p.SetMaxItems = true
		if string(b.MaxItems) != "null" {
			var n int
			if err := json.Unmarshal(b.MaxItems, &n); err != nil || n < 1 {
				return p, errors.New("maxItems must be a positive number or null")
			}
			p.MaxItems = &n
		}
	}
	if b.VoteMode != nil && *b.VoteMode != "thumbs" && *b.VoteMode != "budget" {
		return p, errors.New("voteMode must be thumbs or budget")
	}
	if b.VotesPerMember != nil && (*b.VotesPerMember < 1 || *b.VotesPerMember > maxVotesPerMember) {
		return p, fmt.Errorf("votesPerMember must be between 1 and %d", maxVotesPerMember)
	}
	return p, nil
}

func changesMoreThanLock(p db.SettingsPatch) bool {
	return p.Name != nil || p.SetMaxItems || p.VoteMode != nil || p.VotesPerMember != nil || p.AllowDuplicateVotes != nil
}
