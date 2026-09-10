// Package coverage describes what has actually been read from the paired phone.
package coverage

import "time"

type Folder struct {
	Folder     string    `json:"folder"`
	StartedAt  time.Time `json:"started_at"`
	ScannedAt  time.Time `json:"scanned_at"`
	Complete   bool      `json:"complete"`
	StopReason string    `json:"stop_reason"`
	Pages      int       `json:"pages"`
	PageSize   int       `json:"page_size"`
	Count      int       `json:"count"`
}
type Messages struct {
	Scope              string `json:"scope"`
	Complete           bool   `json:"complete"`
	StopReason         string `json:"stop_reason"`
	RequestedPerThread int    `json:"requested_per_thread"`
	ThreadsFetched     int    `json:"threads_fetched"`
	RecordsImported    int    `json:"records_imported"`
}
type Read struct {
	ContactsImported int      `json:"contacts_imported,omitempty"`
	Source           string   `json:"source"`
	Folder           string   `json:"folder"`
	FolderScans      []Folder `json:"folder_scans"`
	MessageHistory   Messages `json:"message_history"`
	Truncated        bool     `json:"truncated"`
}
type Result[T any] struct {
	Coverage Read `json:"coverage"`
	Data     T    `json:"data"`
}
