package script

import (
	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/script/js"
	"github.com/cordon-dev/cordon/script/python"
)

// Python returns the tool binding for the sandboxed Python interpreter.
func Python() cordon.ToolBinding {
	return python.Tool()
}

// JS returns the tool binding for the sandboxed JavaScript interpreter.
func JS() cordon.ToolBinding {
	return js.Tool()
}

// PythonCommand returns the command for running python inside sandboxed bash.
func PythonCommand() command.Command {
	return python.Command()
}

// Python3Command returns the command for running python3 inside sandboxed bash.
func Python3Command() command.Command {
	return python.Python3Command()
}

// NodeCommand returns the command for running node inside sandboxed bash.
func NodeCommand() command.Command {
	return js.Command()
}

// JSCommand returns the command for running js inside sandboxed bash.
func JSCommand() command.Command {
	return js.JSCommand()
}

// Register registers both python and js tools onto the provided sandbox.
func Register(sb *cordon.Sandbox) {
	sb.RegisterTool(python.Tool())
	sb.RegisterTool(js.Tool())
}
