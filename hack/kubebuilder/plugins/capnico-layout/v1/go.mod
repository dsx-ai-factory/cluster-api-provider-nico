module github.com/dsx-ai-factory/cluster-api-provider-nico/hack/kubebuilder/plugins/capnico-layout/v1

// Independent of the root go.mod's exact-patch pin (see its comment there):
// this module is a local scaffolding tool with no Dockerfile and no
// distributed artifact. govulncheck.yml documents it as intentionally
// unscanned, so it tracks its own minimum instead of the root's
// scan-accuracy floor.
go 1.24
