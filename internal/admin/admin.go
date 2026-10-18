// Package admin implements the admin console HTTP API: the surface the
// dashboard (Next.js, under /admin) talks to. It is mounted by the API server
// under /admin and carries its own authentication — email/password sessions in
// HttpOnly cookies — entirely separate from merchant API-key auth.
//
// Layering note: handlers in this package read directly through the
// repository interfaces for console-specific data and go through the core
// engine for payment-affecting operations (cancel, sync), so payment state
// transitions always pass the engine's guards.
package admin
