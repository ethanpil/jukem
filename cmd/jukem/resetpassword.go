package main

import "errors"

// runResetPassword becomes available in build step 4, when the store and
// the login exist.
func runResetPassword(args []string) error {
	return errors.New("reset-password is not available in this build")
}
