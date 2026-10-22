// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"errors"
	"fmt"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// Encode a document and decode it back. Marshal writes IEEE 2030.5-2023,
// with its schemaVer and the elements the schema requires.
func ExampleMarshal() {
	href := "/edev/1"
	ed := &ieee20305.EndDevice{}
	ed.Href, ed.SFDI = &href, 123456789018
	data, err := ieee20305.Marshal(ed)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
	var back ieee20305.EndDevice
	if err := ieee20305.Unmarshal(data, &back); err != nil {
		panic(err)
	}
	fmt.Println(back.SFDI)
	// Output:
	// <EndDevice xmlns="urn:ieee:std:2030.5:ns" schemaVer="2.2" href="/edev/1"><sFDI>123456789018</sFDI><changedTime>0</changedTime></EndDevice>
	// 123456789018
}

// Classify an error of a client: a 503 may succeed later, a 404 will not.
func ExampleRetryable() {
	unavailable := &ieee20305.StatusError{Method: "GET", Path: "/dcap", StatusCode: 503, RetryAfter: 30 * time.Second}
	notFound := &ieee20305.StatusError{Method: "GET", Path: "/edev/9", StatusCode: 404}
	fmt.Println(ieee20305.Retryable(unavailable), ieee20305.RetryAfter(unavailable))
	fmt.Println(ieee20305.Retryable(notFound), errors.Is(notFound, ieee20305.ErrNotFound))
	// Output:
	// true 30s
	// false true
}

// The delays of an exponential backoff, without jitter.
func ExampleExponentialBackoff() {
	b := ieee20305.ExponentialBackoff{Initial: time.Second, Max: 10 * time.Second, NoJitter: true}
	for attempt := 0; attempt < 5; attempt++ { // 0 is the first call
		fmt.Print(b.Delay(attempt), " ")
	}
	fmt.Println()
	// Output:
	// 1s 2s 4s 8s 10s
}
