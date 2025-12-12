package service

import "context"

type Storer interface {
	Delete(ctx context.Context, id string) error
}
