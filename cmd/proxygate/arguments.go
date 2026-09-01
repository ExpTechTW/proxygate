package main

import "flag"

type arguments struct {
	configPath  string
	showVersion bool
}

func parseCommandLine() arguments {
	var args arguments
	flag.StringVar(&args.configPath, "config", "config.json", "path to the JSON configuration file")
	flag.BoolVar(&args.showVersion, "version", false, "print version information")
	flag.Parse()
	return args
}
