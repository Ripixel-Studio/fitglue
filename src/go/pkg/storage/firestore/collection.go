package firestore

import (
	"context"

	firestore "github.com/fitglue/server/src/go/pkg/docstore"
)

type ToFirestoreFunc[T any] func(*T) map[string]interface{}
type FromFirestoreFunc[T any] func(map[string]interface{}) *T

type Collection[T any] struct {
	Ref           *firestore.CollectionRef
	ToFirestore   ToFirestoreFunc[T]
	FromFirestore FromFirestoreFunc[T]
}

func (c *Collection[T]) Doc(id string) *DocumentRef[T] {
	return &DocumentRef[T]{
		Ref:           c.Ref.Doc(id),
		ToFirestore:   c.ToFirestore,
		FromFirestore: c.FromFirestore,
	}
}

func (c *Collection[T]) NewDoc() *DocumentRef[T] {
	return &DocumentRef[T]{
		Ref:           c.Ref.NewDoc(),
		ToFirestore:   c.ToFirestore,
		FromFirestore: c.FromFirestore,
	}
}

type DocumentRef[T any] struct {
	Ref           *firestore.DocumentRef
	ToFirestore   ToFirestoreFunc[T]
	FromFirestore FromFirestoreFunc[T]
}

func (d *DocumentRef[T]) ID() string {
	return d.Ref.ID
}

func (d *DocumentRef[T]) Get(ctx context.Context) (*T, error) {
	snap, err := d.Ref.Get(ctx)
	if err != nil {
		return nil, err
	}
	return d.FromFirestore(snap.Data()), nil
}

func (d *DocumentRef[T]) Set(ctx context.Context, data *T) error {
	m := d.ToFirestore(data)
	_, err := d.Ref.Set(ctx, m, firestore.MergeAll)
	return err
}

func (d *DocumentRef[T]) Update(ctx context.Context, updates map[string]interface{}) error {
	// Simple map update - keys must match Firestore snake_case fields
	// We do not run converter here because updates are often partials/dots
	// If caller wants type safety, they should construct map carefully or we add TypedUpdate support later.
	_, err := d.Ref.Set(ctx, updates, firestore.MergeAll)
	return err
}
