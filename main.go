/*
 * Copyright (C) 2017 "IoT.bzh"
 * Author Sebastien Douheret <sebastien@iot.bzh>
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 *
 * xds-exec: a wrapper on exec linux command for X(cross) Development System.
 */

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/iotbzh/xds-agent/lib/xaapiv1"
	common "github.com/iotbzh/xds-common/golib"
	"github.com/joho/godotenv"
	socketio_client "github.com/sebd71/go-socket.io-client"
	"github.com/urfave/cli"
)

var appAuthors = []cli.Author{
	cli.Author{Name: "Sebastien Douheret", Email: "sebastien@iot.bzh"},
}

// AppName name of this application
var AppName = "xds-exec"

// AppNativeName native command name that this application can overload
var AppNativeName = "exec"

// AppVersion Version of this application
// (set by Makefile)
var AppVersion = "?.?.?"

// AppSubVersion is the git tag id added to version string
// Should be set by compilation -ldflags "-X main.AppSubVersion=xxx"
// (set by Makefile)
var AppSubVersion = "unknown-dev"

// Create logger
var log = logrus.New()

// Application details
const (
	appCopyright    = "Apache-2.0"
	defaultLogLevel = "error"
)

// exitError exists this program with the specified error
func exitError(code int, f string, a ...interface{}) {
	err := fmt.Sprintf(f, a...)
	fmt.Fprintf(os.Stderr, err+"\n")
	os.Exit(code)
}

// main
func main() {
	var uri, prjID, rPath, logLevel, sdkid, confFile string
	var withTimestamp, listProject bool

	// Allow to set app name from exec (useful for debugging)
	if AppName == "" {
		AppName = os.Getenv("XDS_APPNAME")
	}
	if AppName == "" {
		panic("Invalid setup, AppName not define !")
	}
	if AppNativeName == "" {
		AppNativeName = AppName[4:]
	}
	appUsage := fmt.Sprintf("wrapper on %s for X(cross) Development System.", AppNativeName)

	appDescription := fmt.Sprintf("%s utility of X(cross) Development System\n", AppNativeName)
	appDescription += `
   xds-exec configuration is driven either by environment variables or by command line
   options or using a config file knowning that the following priority order is used:
     1. use option value (for example use project ID set by --id option),
     2. else use variable 'XDS_xxx' (for example 'XDS_PROJECT_ID' variable) when a
        config file is specified with '--config|-c' option,
     3. else use 'XDS_xxx' (for example 'XDS_PROJECT_ID') environment variable.
`

	// Create a new App instance
	app := cli.NewApp()
	app.Name = AppName
	app.Usage = appUsage
	app.Version = AppVersion + " (" + AppSubVersion + ")"
	app.Authors = appAuthors
	app.Copyright = appCopyright
	app.Metadata = make(map[string]interface{})
	app.Metadata["version"] = AppVersion
	app.Metadata["git-tag"] = AppSubVersion
	app.Metadata["logger"] = log

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "id",
			EnvVar:      "XDS_PROJECT_ID",
			Usage:       "project ID you want to build (mandatory variable)",
			Destination: &prjID,
		},
		cli.StringFlag{
			Name:        "config, c",
			EnvVar:      "XDS_CONFIG",
			Usage:       "env config file to source on startup",
			Destination: &confFile,
		},
		cli.BoolFlag{
			Name:        "list, ls",
			Usage:       "list existing projects",
			Destination: &listProject,
		},
		cli.StringFlag{
			Name:        "log, l",
			EnvVar:      "XDS_LOGLEVEL",
			Usage:       "logging level (supported levels: panic, fatal, error, warn, info, debug)",
			Value:       defaultLogLevel,
			Destination: &logLevel,
		},
		cli.StringFlag{
			Name:        "rpath",
			EnvVar:      "XDS_RPATH",
			Usage:       "relative path into project",
			Destination: &rPath,
		},
		cli.StringFlag{
			Name:        "sdkid",
			EnvVar:      "XDS_SDK_ID",
			Usage:       "Cross Sdk ID to use to build project",
			Destination: &sdkid,
		},
		cli.BoolFlag{
			Name:        "timestamp, ts",
			EnvVar:      "XDS_TIMESTAMP",
			Usage:       "prefix output with timestamp",
			Destination: &withTimestamp,
		},
		cli.StringFlag{
			Name:        "url",
			EnvVar:      "XDS_AGENT_URL",
			Value:       "localhost:8000",
			Usage:       "local XDS agent url",
			Destination: &uri,
		},
	}

	// Create env vars help
	dynDesc := "\nENVIRONMENT VARIABLES:"
	for _, f := range app.Flags {
		var env, usage string
		switch f.(type) {
		case cli.StringFlag:
			fs := f.(cli.StringFlag)
			env = fs.EnvVar
			usage = fs.Usage
		case cli.BoolFlag:
			fb := f.(cli.BoolFlag)
			env = fb.EnvVar
			usage = fb.Usage
		default:
			exitError(1, "Un-implemented option type")
		}
		if env != "" {
			dynDesc += fmt.Sprintf("\n %s \t\t %s", env, usage)
		}
	}
	app.Description = appDescription + dynDesc

	args := make([]string, len(os.Args))
	args[0] = os.Args[0]
	argsCommand := make([]string, len(os.Args))
	exeName := filepath.Base(os.Args[0])

	// Just use to debug log
	hostEnv := os.Environ()

	// Split xds-xxx options from native command (eg. make) options
	// only process args before skip arguments, IOW before '--'
	found := false
	envMap := make(map[string]string)
	if exeName != AppNativeName {
		for idx, a := range os.Args[1:] {
			if a == "-c" || a == "--config" {
				// Source config file when set
				confFile = os.Args[idx+2]
				if confFile != "" {
					if !exists(confFile) {
						exitError(1, "Error env config file not found")
					}
					// Load config file variables that will overwrite env variables
					err := godotenv.Overload(confFile)
					if err != nil {
						exitError(1, "Error loading env config file "+confFile)
					}
					envMap, err = godotenv.Read(confFile)
					if err != nil {
						exitError(1, "Error reading env config file "+confFile)
					}
				}
			}
			if a == "--" {
				// Detect skip option (IOW '--') to split arguments
				copy(args, os.Args[0:idx+1])
				copy(argsCommand, os.Args[idx+2:])
				found = true
				goto exit_loop
			}
		}
	exit_loop:
		if !found {
			copy(args, os.Args)
		}
	} else {
		copy(argsCommand, os.Args)
	}

	// only one action
	app.Action = func(ctx *cli.Context) error {
		var err error

		var execCommand, ccHelp string
		switch AppName {
		case "xds-exec":
			execCommand = "/exec"
			ccHelp = "'mkdir build; cd build; cmake ..'"
		default:
			panic("Un-implemented command")
		}

		// Set logger level and formatter
		if log.Level, err = logrus.ParseLevel(logLevel); err != nil {
			msg := fmt.Sprintf("Invalid log level : \"%v\"\n", logLevel)
			return cli.NewExitError(msg, 1)
		}
		log.Formatter = &logrus.TextFormatter{}

		log.Infof("%s version: %s", AppName, app.Version)
		log.Debugf("Environment: %v", hostEnv)
		log.Infof("Execute: %s %v", execCommand, argsCommand)

		// Define HTTP and WS url
		baseURL := uri
		if !strings.HasPrefix(uri, "http://") {
			baseURL = "http://" + uri
		}

		// Create HTTP client
		log.Debugln("Connect HTTP client on ", baseURL)
		conf := common.HTTPClientConfig{
			URLPrefix:           "/api/v1",
			HeaderClientKeyName: "Xds-Agent-Sid",
			CsrfDisable:         true,
		}
		c, err := common.HTTPNewClient(baseURL, conf)
		if err != nil {
			errmsg := err.Error()
			if m, err := regexp.MatchString("Get http.?://", errmsg); m && err == nil {
				i := strings.LastIndex(errmsg, ":")
				errmsg = "Cannot connection to " + baseURL + errmsg[i:]
			}
			return cli.NewExitError(errmsg, 1)
		}

		// First call to check that daemon is alive
		var data []byte
		if err := c.HTTPGet("/version", &data); err != nil {
			return cli.NewExitError(err.Error(), 1)
		}
		log.Infof("XDS Agent/Server version: %v", string(data[:]))

		// Retrieve projects list used by help output
		if err := c.HTTPGet("/projects", &data); err != nil {
			return cli.NewExitError(err.Error(), 1)
		}
		log.Debugf("Result of /projects: %v", string(data[:]))

		projects := []xaapiv1.ProjectConfig{}
		errMar := json.Unmarshal(data, &projects)

		// Check mandatory args
		if prjID == "" || listProject {
			msg := ""
			exc := 0
			if !listProject {
				msg = "XDS_PROJECT_ID environment variable must be set !\n"
				exc = 1
			}
			if errMar == nil {
				msg += "List of existing projects (use: export XDS_PROJECT_ID=<< ID >>): \n"
				msg += "  ID\t\t\t\t | Label"
				for _, f := range projects {
					msg += fmt.Sprintf("\n  %s\t | %s", f.ID, f.Label)
					if f.DefaultSdk != "" {
						msg += fmt.Sprintf("\t(default SDK: %s)", f.DefaultSdk)
					}
				}
				msg += "\n"
			}

			data = nil
			if err := c.HTTPGet("/servers/0/sdks", &data); err != nil {
				return cli.NewExitError(err.Error(), 1)
			}
			log.Debugf("Result of /sdks: %v", string(data[:]))

			sdks := []xaapiv1.SDK{}
			errMar = json.Unmarshal(data, &sdks)
			if errMar == nil {
				msg += "\nList of installed cross SDKs (use: export XDS_SDK_ID=<< ID >>): \n"
				msg += "  ID\t\t\t\t\t | NAME\n"
				for _, s := range sdks {
					msg += fmt.Sprintf("  %s\t | %s\n", s.ID, s.Name)
				}
			}

			if len(projects) > 0 && len(sdks) > 0 {
				msg += fmt.Sprintf("\n")
				msg += fmt.Sprintf("For example: \n")
				msg += fmt.Sprintf("  %s --id %q --sdkid %q -- %s\n", AppName, projects[0].ID, sdks[0].ID, ccHelp)
				msg += " or\n"
				msg += fmt.Sprintf("  XDS_PROJECT_ID=%q XDS_SDK_ID=%q  %s %s\n", projects[0].ID, sdks[0].ID, AppNativeName, ccHelp)
			}

			return cli.NewExitError(msg, exc)
		}

		// Create io Websocket client
		log.Debugln("Connecting IO.socket client on ", baseURL)

		opts := &socketio_client.Options{
			Transport: "websocket",
			Header:    make(map[string][]string),
		}
		opts.Header["XDS-AGENT-SID"] = []string{c.GetClientID()}

		iosk, err := socketio_client.NewClient(baseURL, opts)
		if err != nil {
			return cli.NewExitError("IO.socket connection error: "+err.Error(), 1)
		}

		// Process Socket IO events
		type exitResult struct {
			error error
			code  int
		}
		exitChan := make(chan exitResult, 1)

		iosk.On("error", func(err error) {
			fmt.Println("ERROR: ", err.Error())
		})

		iosk.On("disconnection", func(err error) {
			exitChan <- exitResult{err, 2}
		})

		outFunc := func(timestamp, stdout, stderr string) {
			tm := ""
			if withTimestamp {
				tm = timestamp + "| "
			}
			if withTimestamp {
				tm = timestamp + "| "
			}
			if stdout != "" {
				fmt.Printf("%s%s", tm, stdout)
			}
			if stderr != "" {
				fmt.Fprintf(os.Stderr, "%s%s", tm, stderr)
			}
		}

		iosk.On(xaapiv1.ExecOutEvent, func(ev xaapiv1.ExecOutMsg) {
			outFunc(ev.Timestamp, ev.Stdout, ev.Stderr)
		})

		iosk.On(xaapiv1.ExecExitEvent, func(ev xaapiv1.ExecExitMsg) {
			exitChan <- exitResult{ev.Error, ev.Code}
		})

		// Retrieve the projects definition
		var project *xaapiv1.ProjectConfig
		for _, f := range projects {
			if f.ID == prjID {
				project = &f
				break
			}
		}

		// Auto setup rPath if needed
		if rPath == "" && project != nil {
			cwd, err := os.Getwd()
			if err == nil {
				fldRp := project.ClientPath
				if !strings.HasPrefix(fldRp, "/") {
					fldRp = "/" + fldRp
				}
				log.Debugf("Try to auto-setup rPath: cwd=%s ; ClientPath=%s", cwd, fldRp)
				if sp := strings.SplitAfter(cwd, fldRp); len(sp) == 2 {
					rPath = strings.Trim(sp[1], "/")
					log.Debugf("Auto-setup rPath to: '%s'", rPath)
				}
			}
		}

		// Build env
		log.Debugf("Command env: %v", envMap)
		env := []string{}
		for k, v := range envMap {
			env = append(env, k+"="+v)
		}

		// Send build command
		var body []byte
		args := xaapiv1.ExecArgs{
			ID:         prjID,
			SdkID:      sdkid,
			Cmd:        strings.Trim(argsCommand[0], " "),
			Args:       argsCommand[1:],
			Env:        env,
			RPath:      rPath,
			CmdTimeout: 60,
		}
		body, err = json.Marshal(args)
		if err != nil {
			return cli.NewExitError(err.Error(), 1)
		}
		log.Infof("POST %s%s %v", uri, execCommand, string(body))
		if err := c.HTTPPost(execCommand, string(body)); err != nil {
			return cli.NewExitError(err.Error(), 1)
		}

		// Wait exit
		select {
		case res := <-exitChan:
			errStr := ""
			if res.code == 0 {
				log.Debugln("Exit successfully")
			}
			if res.error != nil {
				log.Debugln("Exit with ERROR: ", res.error.Error())
				errStr = res.error.Error()
			}
			return cli.NewExitError(errStr, res.code)
		}
	}

	app.Run(args)
}

// Exists returns whether the given file or directory exists or not
func exists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}
