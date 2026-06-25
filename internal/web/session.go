// Package web — session.go: anonymous identity middleware.
//
// Responsibility: read the session_id cookie or mint a new UUID once; set an
// HttpOnly, SameSite=Lax cookie; stash the resulting OwnerID in the request
// context. This is the v2 auth seam — only how OwnerID is derived changes later.
package web

// TODO: session middleware (cookie mint/reuse) + OwnerID-from-context helper.
