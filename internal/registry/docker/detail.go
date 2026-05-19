package docker

import (
	"context"

	"github.com/rakunlabs/pika/internal/registry"
)

// buildPackageDetail collects per-image (per-repository) metadata
// into the shared registry.PackageDetail shape. For each tag pointer
// we resolve the manifest, parse its layer / platform shape and
// surface a DockerTagDetail row.
//
// Returns (nil, registry.ErrPackageNotFound) when the named image
// has no tags. A repo with tags but unreadable manifests still
// returns the rows it could resolve, falling back to digest-only
// entries — the operator gets a partial view rather than a hard
// failure.
func buildPackageDetail(_ context.Context, s *Store, name string) (*registry.PackageDetail, error) {
	tags, err := s.ListTags(name)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, registry.ErrPackageNotFound
	}
	out := &registry.PackageDetail{
		Type: "docker",
		Name: name,
		Docker: &registry.DockerRepoDetail{
			Tags: make([]registry.DockerTagDetail, 0, len(tags)),
		},
	}
	for _, t := range tags {
		row := registry.DockerTagDetail{Tag: t}
		dgst, err := s.ReadTag(name, t)
		if err != nil {
			// Dangling tag pointer; surface tag name only.
			out.Docker.Tags = append(out.Docker.Tags, row)
			continue
		}
		row.Digest = dgst.String()
		rec, err := s.ReadManifest(name, dgst)
		if err != nil {
			out.Docker.Tags = append(out.Docker.Tags, row)
			continue
		}
		row.MediaType = rec.ContentType
		row.ManifestSize = int64(len(rec.Body))
		if artType := ArtifactTypeOf(rec.Body); artType != "" {
			row.ArtifactType = artType
		}
		if insp := InspectManifestBytes(rec.Body); insp != nil {
			row.ConfigDigest = insp.ConfigDigest
			row.ImageSize = insp.ImageSize
			if len(insp.Layers) > 0 {
				row.Layers = make([]registry.DockerLayer, 0, len(insp.Layers))
				for _, l := range insp.Layers {
					row.Layers = append(row.Layers, registry.DockerLayer{
						Digest:    l.Digest,
						Size:      l.Size,
						MediaType: l.MediaType,
					})
				}
			}
			if len(insp.Platforms) > 0 {
				row.Platforms = make([]registry.DockerPlatform, 0, len(insp.Platforms))
				for _, p := range insp.Platforms {
					row.Platforms = append(row.Platforms, registry.DockerPlatform{
						OS:           p.OS,
						Architecture: p.Architecture,
						Variant:      p.Variant,
						Digest:       p.Digest,
						Size:         p.Size,
					})
				}
			}
		}
		out.Docker.Tags = append(out.Docker.Tags, row)
	}
	return out, nil
}
