package api

import (
	"net/http"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/store"
)

// endUserHeader is the header an integrator sets to attribute a publish to
// one of its own users.
const endUserHeader = "X-Artifact-End-User"

const maxEndUserID = 128

// endUserFrom validates the optional X-Artifact-End-User header: 1..128
// printable ASCII characters. An absent header yields "".
func endUserFrom(r *http.Request) (string, *apierr.Error) {
	v := r.Header.Get(endUserHeader)
	if v == "" {
		return "", nil
	}
	if len(v) > maxEndUserID {
		return "", apierr.Invalid(endUserHeader + " must be at most 128 characters")
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return "", apierr.Invalid(endUserHeader + " must be printable ASCII")
		}
	}
	return v, nil
}

// attachEndUser records the end user on a new artifact when the header is set.
func (s *server) attachEndUser(r *http.Request, a store.Artifact, tenantID, external string) (store.Artifact, *apierr.Error) {
	if external == "" {
		return a, nil
	}
	eu, err := s.Store.UpsertEndUser(r.Context(), tenantID, external, s.Now())
	if err != nil {
		return a, apierr.Internal(err)
	}
	a.EndUserID = &eu.ID
	a.EndUser = &external
	return a, nil
}

// checkEndUser rejects an update that names a different end user than the
// artifact belongs to. An artifact with no end user is treated as belonging
// to nobody, so naming one on update is also rejected.
func checkEndUser(a store.Artifact, external string) *apierr.Error {
	if external == "" {
		return nil
	}
	if a.EndUser == nil || *a.EndUser != external {
		return apierr.Forbidden("artifact belongs to another end user")
	}
	return nil
}
