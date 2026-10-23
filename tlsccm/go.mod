module github.com/otfabric/go-ieee20305/tlsccm

go 1.26.8

require (
	github.com/otfabric/go-ieee20305 v0.5.2
	golang.org/x/crypto v0.57.0
)

require golang.org/x/sys v0.48.0 // indirect

// The library of this repository, as it is here.
replace github.com/otfabric/go-ieee20305 => ../
