// Package env loads production configuration for Meta applications.
package env

import (
	"fmt"
	"os"

	"github.com/akashKarmakar02/meta/internal/envfile"
)

// LoadProduction loads .env and then .env.production from root. Existing
// process environment variables always win, including empty values. Call it
// once during application startup, before starting concurrent work. Errors
// identify a file and line without revealing the value.
func LoadProduction(root string) error {
	values, err := envfile.Read(root, "production")
	if err != nil {
		return fmt.Errorf("meta production environment: %w", err)
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("meta production environment: set %s: %w", key, err)
		}
	}
	return nil
}
