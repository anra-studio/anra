// Package tapestorage enforces Anra's bbolt storage backend.
package tapestorage

import (
	"errors"
	"reflect"

	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
)

// RequireBbolt accepts bbolt and storage decorators backed by bbolt. The bound
// rejects cyclic or excessively deep decorator chains without hanging startup.
func RequireBbolt(tape storage.TapeStorage) error {
	for depth := 0; depth < 64 && tape != nil; depth++ {
		v := reflect.ValueOf(tape)
		switch v.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if v.IsNil() {
				return errors.New("anra: storage must be backed by a non-nil bbolt tape")
			}
		}
		if _, ok := tape.(*bbolt.Bbolt); ok {
			return nil
		}
		wrapper, ok := tape.(storage.Unwrapper)
		if !ok {
			break
		}
		tape = wrapper.Unwrap()
	}
	return errors.New("anra: storage must be backed by a non-nil bbolt tape")
}
