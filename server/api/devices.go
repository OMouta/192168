package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
	"github.com/OMouta/192168/server/storage"
)

// handleRegisterDevice is the only unauthenticated write in the API, so it does
// its own proving. The request is signed by the key it registers, the timestamp
// has to be recent, and the nonce has to be unused. Together those stop a
// captured registration from being replayed.
func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	if !s.devices.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, api.ErrRateLimited, "Too many attempts. Wait a moment and try again.")
		return
	}

	var req api.RegisterDeviceRequest
	if !decode(w, r, &req) {
		return
	}
	if req.DeviceID == "" || req.PublicKey == "" || req.TransportKey == "" || req.Nonce == "" {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "The registration is missing required fields.")
		return
	}

	issuedAt := time.Unix(req.IssuedAt, 0)
	if skew := time.Since(issuedAt); skew > auth.RegisterMaxSkew || skew < -auth.RegisterMaxSkew {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "This device's clock is too far off. Check the system time.")
		return
	}

	if err := auth.VerifyRegister(req.PublicKey, req.DeviceID, req.TransportKey, req.Signature, issuedAt, req.Nonce); err != nil {
		writeError(w, http.StatusUnauthorized, api.ErrUnauthorized, "The registration could not be verified.")
		return
	}

	// Claiming the nonce after the signature checks out means an attacker
	// cannot burn nonces with junk requests.
	fresh, err := s.store.ClaimRegisterNonce(r.Context(), req.Nonce, 2*auth.RegisterMaxSkew)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}
	if !fresh {
		writeError(w, http.StatusUnauthorized, api.ErrUnauthorized, "The registration could not be verified.")
		return
	}

	// A device with no name yet answers to the machine it runs on. Registering
	// again leaves a chosen name alone.
	token, err := s.store.RegisterDevice(r.Context(), storage.Device{
		ID:           req.DeviceID,
		PublicKey:    req.PublicKey,
		TransportKey: req.TransportKey,
		Name:         req.DeviceName,
		Nickname:     strings.TrimSpace(req.DeviceName),
	})
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, http.StatusConflict, api.ErrBadRequest, "That key is already registered to another device.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("device registered", "deviceId", req.DeviceID)
	writeJSON(w, http.StatusCreated, api.RegisterDeviceResponse{DeviceToken: token})
}
