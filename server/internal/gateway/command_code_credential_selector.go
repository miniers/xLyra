package gateway

import (
	"sync"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

type commandCodeStickySelector struct {
	mu           sync.RWMutex
	activeBySite map[uuid.UUID]uuid.UUID
}

var globalCommandCodeStickySelector = &commandCodeStickySelector{
	activeBySite: map[uuid.UUID]uuid.UUID{},
}

func orderCommandCodeStickyCredentials(siteID uuid.UUID, credentials []store.GatewayCredential) []store.GatewayCredential {
	if len(credentials) <= 1 {
		return credentials
	}

	globalCommandCodeStickySelector.mu.RLock()
	activeID, exists := globalCommandCodeStickySelector.activeBySite[siteID]
	globalCommandCodeStickySelector.mu.RUnlock()

	if !exists || activeID == uuid.Nil {
		return credentials
	}

	activeIdx := -1
	for i, cred := range credentials {
		if cred.Credential.ID == activeID {
			activeIdx = i
			break
		}
	}

	if activeIdx <= 0 {
		return credentials
	}

	// Move active credential to the front to maintain Prompt Cache sticky affinity
	reordered := make([]store.GatewayCredential, 0, len(credentials))
	reordered = append(reordered, credentials[activeIdx])
	reordered = append(reordered, credentials[:activeIdx]...)
	reordered = append(reordered, credentials[activeIdx+1:]...)
	return reordered
}

func recordCommandCodeCredentialSuccess(siteID, credentialID uuid.UUID) {
	if siteID == uuid.Nil || credentialID == uuid.Nil {
		return
	}
	globalCommandCodeStickySelector.mu.Lock()
	globalCommandCodeStickySelector.activeBySite[siteID] = credentialID
	globalCommandCodeStickySelector.mu.Unlock()
}

func recordCommandCodeCredentialFailure(siteID, credentialID uuid.UUID) {
	if siteID == uuid.Nil || credentialID == uuid.Nil {
		return
	}
	globalCommandCodeStickySelector.mu.Lock()
	if globalCommandCodeStickySelector.activeBySite[siteID] == credentialID {
		delete(globalCommandCodeStickySelector.activeBySite, siteID)
	}
	globalCommandCodeStickySelector.mu.Unlock()
}
