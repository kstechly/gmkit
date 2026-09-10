package gm

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/johnlindquist/gmkit/internal/coverage"
)

// ArchiveCheck records agreement between independent status and folder reads.
type ArchiveCheck struct {
	ID        string `json:"conversation_id"`
	Status    string `json:"individual_status"`
	InInbox   bool   `json:"in_inbox"`
	InArchive bool   `json:"in_archive"`
	Verified  bool   `json:"verified"`
}

// ArchiveVerification is an observation at read time, not a durable state guarantee.
type ArchiveVerification struct {
	Source      string            `json:"source"`
	CheckedAt   time.Time         `json:"checked_at"`
	FolderScans []coverage.Folder `json:"folder_scans"`
	Checks      []ArchiveCheck    `json:"checks"`
	Verified    bool              `json:"verified"`
}

// VerifyArchives reads both folders to completion and checks every target individually.
func (c *Client) VerifyArchives(ctx context.Context, ids []string) (r ArchiveVerification, err error) {
	r = ArchiveVerification{Source: "live", FolderScans: []coverage.Folder{}, Checks: []ArchiveCheck{}}
	defer func() { r.CheckedAt = time.Now().UTC() }()
	if len(ids) == 0 {
		return r, fmt.Errorf("at least one conversation ID is required")
	}
	in, e := c.ScanFolder(ctx, "inbox", 25, 1000)
	r.FolderScans = append(r.FolderScans, in.Folder)
	if e != nil {
		return r, e
	}
	ar, e := c.ScanFolder(ctx, "archive", 25, 1000)
	r.FolderScans = append(r.FolderScans, ar.Folder)
	if e != nil {
		return r, e
	}
	inbox, archive := map[string]bool{}, map[string]bool{}
	for _, v := range in.Conversations {
		inbox[v.GetConversationID()] = true
	}
	for _, v := range ar.Conversations {
		archive[v.GetConversationID()] = true
	}
	r.Verified = true
	for _, id := range ids {
		v, e := c.ReadConversation(ctx, id)
		if e != nil {
			r.Verified = false
			return r, e
		}
		check := checkArchive(id, v.GetStatus(), inbox[id], archive[id])
		r.Checks = append(r.Checks, check)
		r.Verified = r.Verified && check.Verified
	}
	if !r.Verified {
		return r, fmt.Errorf("archive verification disagrees: inspect individual_status, in_inbox and in_archive; no mutation was sent")
	}
	return r, nil
}
func checkArchive(id string, status gmproto.ConversationStatus, inbox, archive bool) ArchiveCheck {
	return ArchiveCheck{ID: id, Status: status.String(), InInbox: inbox, InArchive: archive, Verified: (status == gmproto.ConversationStatus_ARCHIVED || status == gmproto.ConversationStatus_KEEP_ARCHIVED) && !inbox && archive}
}
