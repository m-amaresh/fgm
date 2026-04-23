package cli

import "github.com/fatih/color"

var (
	green = color.New(color.Bold, color.FgGreen).SprintFunc()
	blue  = color.New(color.Bold, color.FgBlue).SprintFunc()
)
