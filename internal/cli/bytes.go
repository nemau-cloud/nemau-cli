package cli

import (
	"bytes"
	"fmt"
	"io"
)

// maxUploadBytes mirrors the API's per-object cap, checked client-side so an
// oversized file fails before it is read rather than after it is sent.
const maxUploadBytes = 100 * 1024 * 1024

func newBytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// humanBytes renders a size in binary units (KiB, MiB, GiB and so on).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
