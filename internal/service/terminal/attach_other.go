//go:build !linux

package terminal

import (
	"context"
	"fmt"
	"os"
)

type Attachment struct{ File *os.File }

func Attach(context.Context, string, string, uint16, uint16) (*Attachment, error) {
	return nil, fmt.Errorf("host terminals require Linux")
}
func (a *Attachment) Resize(uint16, uint16) error { return fmt.Errorf("host terminals require Linux") }
func (a *Attachment) Close()                      {}

func ResizeWindow(context.Context, string, uint16, uint16) {}

func lockSession(context.Context, string) (func(), error) {
	return nil, fmt.Errorf("host terminals require Linux")
}
