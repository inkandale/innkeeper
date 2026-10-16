package main

import (
	"os"

	"github.com/inkandale/innkeeper/internal/api"
)

// openapi prints the API's OpenAPI spec, for generating clients.
func openapi() error {
	b, err := api.Spec()
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}
