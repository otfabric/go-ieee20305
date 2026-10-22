module github.com/otfabric/go-ieee20305/interop

go 1.26.8

require (
	github.com/otfabric/go-ieee20305 v0.5.0
	github.com/otfabric/go-ieee20305/tlsccm v0.0.0
)

require (
	github.com/GRIDAPPSD/ieee-2030_5-core-go v0.27.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

// The library and the CCM_8 module of this repository, as they are here.
replace (
	github.com/otfabric/go-ieee20305 => ../
	github.com/otfabric/go-ieee20305/tlsccm => ../tlsccm
)
