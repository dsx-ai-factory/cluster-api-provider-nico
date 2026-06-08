# CAPNICo Kubebuilder Layout Plugin

`capnico-layout/v1` is a Kubebuilder external plugin used with `go/v4`.

Kubebuilder `go/v4` scaffolds controllers under `internal/controller`. CAPNICo
keeps controllers at the repository root in `controllers`, matching the Cluster
API provider layout used by CAPA and CAPG. This plugin lets Kubebuilder continue
to scaffold the API/controller files while adapting generated controller files
and generated controller tests into the CAPNICo layout.

## Install

```bash
make install
```

## Use

```bash
make init-project
make create-api KIND=NicoCluster
```
