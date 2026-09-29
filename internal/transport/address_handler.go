package transport

import (
	"net/http"

	"dvbs/internal/model"
	"dvbs/internal/service"
)

type AddressHandler struct {
	svc *service.AddressService
}

func NewAddressHandler(svc *service.AddressService) *AddressHandler {
	return &AddressHandler{svc: svc}
}

func (h *AddressHandler) ListByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	if !authorizeOwner(w, r, int64(userID)) {
		return
	}

	addresses, err := h.svc.ListByUser(r.Context(), userID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, addresses)
}

func (h *AddressHandler) AddToUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	if !authorizeOwner(w, r, int64(userID)) {
		return
	}

	var address model.Address
	if err := decodeJSON(r, &address); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	address.UserID = int64(userID)

	created, err := h.svc.Create(r.Context(), &address)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AddressHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	address, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, address.UserID) {
		return
	}
	writeJSON(w, http.StatusOK, address)
}

func (h *AddressHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, existing.UserID) {
		return
	}

	var address model.Address
	if err := decodeJSON(r, &address); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	address.UserID = existing.UserID
	address.ID = int64(id)

	updated, err := h.svc.Update(r.Context(), id, &address)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AddressHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, existing.UserID) {
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
