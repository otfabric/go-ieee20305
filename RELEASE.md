# go-ieee20305 Releases

## v0.1.0

**Date:** unreleased
**Previous release:** none

## Summary

Initial scaffolding of the IEEE 2030.5 library: domain model types, package layout, build infrastructure, and CI configuration.

## Changes

### Added

- **Domain model** — root package with IEEE 2030.5 resource types: EndDevice, DERProgram, DERControl, DefaultDERControl, DERCapability, DERSettings, DERStatus, DERAvailability, DERCurve, UsagePoint, MeterReading, Reading, ReadingType, MirrorUsagePoint, Registration, DeviceCapability, TimeResource, Subscription, Notification, Response, and the primitive types they are built from (HexBinary160, SFDIType, ActivePower, ReactivePower, Primacy, etc.).
- **Package placeholders** — `client`, `server`, `security`, `schedule`, `discovery`, `internal/xml`, `e2e`, `interop`, `examples/client`.
- **Build infrastructure** — Makefile, `.golangci.yml`, `.gitignore`, CI and release workflows.
- **Documentation** — README with badges, CONTRIBUTING.md, SECURITY.md, RELEASE.md, LICENSE (MIT).
