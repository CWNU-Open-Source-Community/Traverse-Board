# Desktop legacy permission fixture

This closed SQLite database was created by applying the genuine immutable
migration prefix through v177, then inserting a v1 full_access selection
under those historical constraints. It also contains an approved shell proposal
and a Run execution lease; neither is process-local authority after reopening.

- Generator baseline: 1a537b643f596dd65da5170f943dd85640b83f31
  (reviewed C 85dc26e8 and B12 699f26c0, plus checksum-only A15a).
- Raw SQLite size: 7643136 bytes; SHA256: 130b22b8db230627cbc1838b6c0af81975c090d01cb872a0d86a926f635e9f5f.
- Bzip2 SHA256: 1b85c0742f885232cd073c4dff1632cb0b8711313ae3be22542801cfa7fa9abe.
- JSON records are the exact expected historical identities and values.
- No current-schema trigger was dropped or relaxed to produce old rows.

To reproduce a semantically equivalent fixture, use the adjacent
legacy-permission-v177-generator.go.txt as a Go overlay at
internal/store/a15_export_fixture_test.go. Set A15_FIXTURE_EXPORT to a new,
empty task-owned directory and select only
TestA15ExportHistoricalDesktopReopenFixture in ./internal/store.
The generator depends on the store's historical test helpers, uses local
SQLite only and performs no model/tool execution. Do not run desktop, app
or sandbox test binaries on a host where TestMain ACL setup is prohibited.

Generated identities/timestamps vary. Review the exporter result, confirm
schema 177 and foreign keys, then bzip2-compress desktop-v177.db, copy its
facts.json, and update the pinned digest and size in control_plane_test.go.
The consumer checks raw bytes, ledger boundaries and foreign keys before
calling the ordinary control-plane opener. It does not downgrade a current
database or synthesize legacy data through the current public writer.
