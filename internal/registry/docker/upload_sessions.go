package docker

import (
	"sync"
)

// uploadSessions tracks in-flight Docker uploads: Docker UUID →
// (repo name, BlobStore session ID). Kept in-memory per Registry
// instance; on restart, pending uploads are lost (Docker clients
// retry the whole layer when their POST 202 → PATCH chain breaks,
// so the data loss is invisible to clients).
//
// A future enhancement persists the table to rawfs so uploads
// survive a pika restart. For MVP the in-memory cost (one row per
// pending upload) is negligible.
type uploadSessions struct {
	mu     sync.Mutex
	byUUID map[string]uploadEntry
}

type uploadEntry struct {
	RepoName     string
	BlobSession  string
}

func newUploadSessions() *uploadSessions {
	return &uploadSessions{byUUID: make(map[string]uploadEntry)}
}

func (u *uploadSessions) put(uuid, repoName, blobSession string) {
	u.mu.Lock()
	u.byUUID[uuid] = uploadEntry{RepoName: repoName, BlobSession: blobSession}
	u.mu.Unlock()
}

func (u *uploadSessions) get(uuid string) (uploadEntry, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	e, ok := u.byUUID[uuid]
	return e, ok
}

func (u *uploadSessions) delete(uuid string) {
	u.mu.Lock()
	delete(u.byUUID, uuid)
	u.mu.Unlock()
}
