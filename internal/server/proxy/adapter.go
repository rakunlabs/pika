package proxy

import (
	"context"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// ServiceAdapter satisfies ServiceDeps using a real *service.Service.
// Lives here (rather than inline in server.go) so the proxy package
// owns the full dependency surface, including the test fakes.
type ServiceAdapter struct {
	S *service.Service
}

// ServiceFromService is the production constructor used by the server
// boot path.
func ServiceFromService(s *service.Service) *ServiceAdapter {
	return &ServiceAdapter{S: s}
}

func (a *ServiceAdapter) GetData(ctx context.Context, key, versionStr, variant string) (*service.DataResult, error) {
	return a.S.GetData(ctx, key, versionStr, variant)
}

func (a *ServiceAdapter) ConvertFormat(in []byte, from, to string) ([]byte, error) {
	return service.ConvertFormat(in, from, to)
}

func (a *ServiceAdapter) ReadExternal(ctx context.Context, resource, path string) (*external.Entry, error) {
	return a.S.ReadExternal(ctx, resource, path)
}

func (a *ServiceAdapter) WriteExternal(ctx context.Context, resource, path string, data map[string]any) error {
	return a.S.WriteExternal(ctx, resource, path, data)
}

func (a *ServiceAdapter) DeleteExternal(ctx context.Context, resource, path string) error {
	return a.S.DeleteExternal(ctx, resource, path)
}

func (a *ServiceAdapter) ValidateToken(ctx context.Context, raw, scope, op string) error {
	return a.S.ValidateToken(ctx, raw, scope, op)
}
