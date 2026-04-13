// Package jtime provides timezone-aware time utilities.
// Use New to create a Clock bound to a specific location,
// then pass the Clock to components that need timezone-aware time.
package jtime

import (
	"time"
)

// Clock はタイムゾーンを保持する時刻ユーティリティ。
type Clock struct {
	loc *time.Location
}

// New は指定されたタイムゾーンで Clock を生成する。
// loc が nil の場合は UTC を使用する。
func New(loc *time.Location) *Clock {
	if loc == nil {
		loc = time.UTC
	}
	return &Clock{loc: loc}
}

// Now は設定されたタイムゾーンでの現在時刻を返す。
func (c *Clock) Now() time.Time {
	return time.Now().In(c.loc)
}

// Location は設定されたタイムゾーンを返す。
func (c *Clock) Location() *time.Location {
	return c.loc
}

// In は t を設定されたタイムゾーンに変換する。
func (c *Clock) In(t time.Time) time.Time {
	return t.In(c.loc)
}
