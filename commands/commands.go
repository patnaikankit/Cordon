package commands

import "github.com/cordon-dev/cordon/command"

// All returns all built-in commands supported by Cordon.
func All() []command.Command {
	return []command.Command{
		// Navigation and File Operations
		Cat,
		Ls,
		Pwd,
		Mkdir,
		Rm,
		Cp,
		Mv,

		// Text Processing
		Head,
		Tail,
		Wc,
		Grep,
		Sort,
		Uniq,
		Cut,
		Tr,

		// Data and Checksums
		Base64,
		Sha256sum,
	}
}

// Core is an alias for All(), returning the standard core utility commands.
func Core() []command.Command {
	return All()
}
