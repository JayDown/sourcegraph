package dbstore

import (
	"context"
	"database/sql"

	"github.com/keegancsmith/sqlf"
	"github.com/opentracing/opentracing-go/log"

	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/stores/lsifstore"
	"github.com/sourcegraph/sourcegraph/internal/db/basestore"
	"github.com/sourcegraph/sourcegraph/internal/db/batch"
	"github.com/sourcegraph/sourcegraph/internal/observation"
)

// scanPackageReferences scans a slice of package references from the return value of `*Store.query`.
func scanPackageReferences(rows *sql.Rows, queryErr error) (_ []lsifstore.PackageReference, err error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer func() { err = basestore.CloseRows(rows, err) }()

	var references []lsifstore.PackageReference
	for rows.Next() {
		var reference lsifstore.PackageReference
		if err := rows.Scan(
			&reference.DumpID,
			&reference.Scheme,
			&reference.Name,
			&reference.Version,
			&reference.Filter,
		); err != nil {
			return nil, err
		}

		references = append(references, reference)
	}

	return references, nil
}

// SameRepoPager returns a ReferencePager for dumps that belong to the given repository and commit and reference the package with the
// given scheme, name, and version.
func (s *Store) SameRepoPager(ctx context.Context, repositoryID int, commit, scheme, name, version string, limit int) (_ int, _ ReferencePager, err error) {
	ctx, endObservation := s.operations.sameRepoPager.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.Int("repositoryID", repositoryID),
		log.String("commit", commit),
		log.String("scheme", scheme),
		log.String("name", name),
		log.String("version", version),
		log.Int("limit", limit),
	}})
	defer endObservation(1, observation.Args{})

	tx, err := s.transact(ctx)
	if err != nil {
		return 0, nil, err
	}

	conds := []*sqlf.Query{
		sqlf.Sprintf("r.scheme = %s", scheme),
		sqlf.Sprintf("r.name = %s", name),
		sqlf.Sprintf("r.version = %s", version),
		sqlf.Sprintf("r.dump_id IN (%s)", makeVisibleUploadsQuery(repositoryID, commit)),
	}

	totalCount, _, err := basestore.ScanFirstInt(tx.Store.Query(ctx, sqlf.Sprintf(sameRepoPagerCountQuery, sqlf.Join(conds, " AND "))))
	if err != nil {
		return 0, nil, tx.Done(err)
	}

	pageFromOffset := func(ctx context.Context, offset int) ([]lsifstore.PackageReference, error) {
		return scanPackageReferences(tx.Store.Query(ctx, sqlf.Sprintf(sameRepoPagerQuery, sqlf.Join(conds, " AND "), limit, offset)))
	}

	return totalCount, newReferencePager(pageFromOffset, tx.Done), nil
}

const sameRepoPagerCountQuery = `
-- source: enterprise/internal/codeintel/stores/dbstore/references.go:SameRepoPager
SELECT COUNT(*) FROM lsif_references r WHERE %s
`

const sameRepoPagerQuery = `
-- source: enterprise/internal/codeintel/stores/dbstore/references.go:SameRepoPager
SELECT d.id, r.scheme, r.name, r.version, r.filter FROM lsif_references r
LEFT JOIN lsif_dumps_with_repository_name d ON d.id = r.dump_id
WHERE %s ORDER BY d.root LIMIT %d OFFSET %d
`

// PackageReferencePager returns a ReferencePager for dumps that belong to a remote repository (distinct from the given repository id)
// and reference the package with the given scheme, name, and version. All resulting dumps are visible at the tip of their repository's
// default branch.
func (s *Store) PackageReferencePager(ctx context.Context, scheme, name, version string, repositoryID, limit int) (_ int, _ ReferencePager, err error) {
	ctx, endObservation := s.operations.packageReferencePager.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("scheme", scheme),
		log.String("name", name),
		log.String("version", version),
		log.Int("repositoryID", repositoryID),
		log.Int("limit", limit),
	}})
	defer endObservation(1, observation.Args{})

	tx, err := s.transact(ctx)
	if err != nil {
		return 0, nil, err
	}

	conds := []*sqlf.Query{
		sqlf.Sprintf("r.scheme = %s", scheme),
		sqlf.Sprintf("r.name = %s", name),
		sqlf.Sprintf("r.version = %s", version),
		sqlf.Sprintf("d.repository_id != %s", repositoryID),
		sqlf.Sprintf("EXISTS (SELECT 1 FROM lsif_uploads_visible_at_tip WHERE repository_id = d.repository_id AND upload_id = d.id)"),
	}

	totalCount, _, err := basestore.ScanFirstInt(tx.Store.Query(ctx, sqlf.Sprintf(packageReferencePagerCountQuery, sqlf.Join(conds, " AND "))))
	if err != nil {
		return 0, nil, tx.Done(err)
	}

	pageFromOffset := func(ctx context.Context, offset int) ([]lsifstore.PackageReference, error) {
		return scanPackageReferences(tx.Store.Query(ctx, sqlf.Sprintf(packageReferencePagerQuery, sqlf.Join(conds, " AND "), limit, offset)))
	}

	return totalCount, newReferencePager(pageFromOffset, tx.Done), nil
}

const packageReferencePagerCountQuery = `
-- source: enterprise/internal/codeintel/stores/dbstore/references.go:PackageReferencePager
SELECT COUNT(*) FROM lsif_references r
LEFT JOIN lsif_dumps_with_repository_name d ON d.id = r.dump_id
WHERE %s
`

const packageReferencePagerQuery = `
-- source: enterprise/internal/codeintel/stores/dbstore/references.go:PackageReferencePager
SELECT d.id, r.scheme, r.name, r.version, r.filter FROM lsif_references r
LEFT JOIN lsif_dumps_with_repository_name d ON d.id = r.dump_id
WHERE %s ORDER BY d.repository_id, d.root LIMIT %d OFFSET %d
`

// UpdatePackageReferences inserts reference data tied to the given upload.
func (s *Store) UpdatePackageReferences(ctx context.Context, references []lsifstore.PackageReference) (err error) {
	ctx, endObservation := s.operations.updatePackageReferences.With(ctx, &err, observation.Args{LogFields: []log.Field{}})
	defer endObservation(1, observation.Args{})

	if len(references) == 0 {
		return nil
	}

	inserter := batch.NewBatchInserter(ctx, s.Store.Handle().DB(), "lsif_references", "dump_id", "scheme", "name", "version", "filter")
	for _, r := range references {
		filter := r.Filter
		// avoid not null constraint
		if r.Filter == nil {
			filter = []byte{}
		}

		if err := inserter.Insert(ctx, r.DumpID, r.Scheme, r.Name, r.Version, filter); err != nil {
			return err
		}
	}

	return inserter.Flush(ctx)
}
