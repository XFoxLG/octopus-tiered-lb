package model

import (
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewChannelUID generates the immutable identifier used to correlate channel
// configuration with evidence across renames and endpoint edits.
func NewChannelUID() string {
	return uuid.NewString()
}

// EnsureChannelUID fills an empty identifier in memory and reports whether a
// value was generated. Persistence is the caller's responsibility for rows that
// did not go through the GORM create hook (migration and backup imports).
func (c *Channel) EnsureChannelUID() bool {
	if c == nil || strings.TrimSpace(c.ChannelUID) != "" {
		return false
	}
	c.ChannelUID = NewChannelUID()
	return true
}

// BeforeCreate keeps normal channel creation from depending on the caller.
// Old binaries and map-based backup inserts may still write NULL; migration 085
// and the runtime cache refresh repair those rows.
func (c *Channel) BeforeCreate(*gorm.DB) error {
	c.EnsureChannelUID()
	return nil
}
