package policy

// knownOperands reports whether the arguments of a checked read command use
// only known options. cmp compares exactly two files.
func knownOperands(name string, args []string) bool {
	return checkedOptions[name].known(args) && (name != "cmp" || len(args) == 2)
}
