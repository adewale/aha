package depot

import (
	"context"
	"io"
)

type prefixedStore struct {
	base   objectStore
	prefix string
}

func (s *prefixedStore) key(key string) string { return s.prefix + key }
func (s *prefixedStore) get(ctx context.Context, key string) ([]byte, string, error) {
	return s.base.get(ctx, s.key(key))
}
func (s *prefixedStore) getStream(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.base.getStream(ctx, s.key(key))
}
func (s *prefixedStore) putFileIfAbsent(ctx context.Context, key, contentType, path string) (bool, error) {
	return s.base.putFileIfAbsent(ctx, s.key(key), contentType, path)
}
func (s *prefixedStore) putBytesIfAbsent(ctx context.Context, key, contentType string, body []byte) (bool, error) {
	return s.base.putBytesIfAbsent(ctx, s.key(key), contentType, body)
}
func (s *prefixedStore) putBytesConditional(ctx context.Context, key, contentType string, body []byte, etag string) error {
	return s.base.putBytesConditional(ctx, s.key(key), contentType, body, etag)
}
func (s *prefixedStore) exists(ctx context.Context, key string) (bool, error) {
	return s.base.exists(ctx, s.key(key))
}
func (s *prefixedStore) listKeys(ctx context.Context, prefix string) ([]string, error) {
	lister, ok := s.base.(objectLister)
	if !ok {
		return nil, nil
	}
	keys, err := lister.listKeys(ctx, s.key(prefix))
	if err != nil {
		return nil, err
	}
	for i := range keys {
		keys[i] = keys[i][len(s.prefix):]
	}
	return keys, nil
}
