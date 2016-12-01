package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/lair-framework/api-server/client"
	lair "github.com/lair-framework/go-lair"
	reconng "github.com/lair-framework/go-recon-ng"
)

const (
	version = "1.0.0"
	tool    = "recon-ng"
	usage   = `
	Parses a recon-ng JSON file into a lair project.
	Usage:
	drone-recon-ng [options] <id> <filename>
	export LAIR_ID=<id>; drone-recon-ng [options] <filename>
	Options:
	-v              show version and exit
	-h              show usage and exit
	-k              allow insecure SSL connections
	-force-ports    disable data protection in the API server for excessive ports
	-force-hosts    only import hosts that have listening ports
	-tags           a comma separated list of tags to add to every host that is imported
	`
)

func main() {
	showVersion := flag.Bool("v", false, "")
	insecureSSL := flag.Bool("k", false, "")
	forcePorts := flag.Bool("force-ports", false, "")
	forceHosts := flag.Bool("force-hosts", false, "")
	tags := flag.String("tags", "", "")
	flag.Usage = func() {
		fmt.Println(usage)
	}
	flag.Parse()

	if *showVersion {
		log.Println(version)
		os.Exit(0)
	}

	tagSet := map[string]bool{}
	lairURL := os.Getenv("LAIR_API_SERVER")

	if lairURL == "" {
		log.Fatal("Fatal: Missing LAIR_API_SERVER environment variable")
	}

	lairPID := os.Getenv("LAIR_ID")
	var filename string
	switch len(flag.Args()) {
	case 2:
		lairPID = flag.Arg(0)
		filename = flag.Arg(1)
	case 1:
		filename = flag.Arg(0)
	default:
		log.Fatal("Fatal: Missing required argument")
	}

	if lairPID == "" {
		log.Fatal("Fatal: Missing LAIR_ID")
	}

	u, err := url.Parse(lairURL)
	if err != nil {
		log.Fatalf("Fatal: Error parsing LAIR_API_SERVER URL. Error %s", err.Error())
	}

	if u.User == nil {
		log.Fatal("Fatal: Missing username and/or password")
	}

	user := u.User.Username()
	pass, _ := u.User.Password()
	if user == "" || pass == "" {
		log.Fatal("Fatal: Missing username and/or password")
	}
	c, err := client.New(&client.COptions{
		User:               user,
		Password:           pass,
		Host:               u.Host,
		Scheme:             u.Scheme,
		InsecureSkipVerify: *insecureSSL,
	})

	if err != nil {
		log.Fatalf("Fatal: Error setting up client: Error %s\n", err.Error())
	}

	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("Fatal: Could not open file. Error %s\n", err.Error())
	}

	rNotFound := map[string][]reconng.Host{}
	recData, err := reconng.Parse(buf)
	if err != nil {
		log.Fatalf("Fatal: Error parsing recon-ng data. Error %s\n", err.Error())
	}
	hostTags := []string{}
	if *tags != "" {
		hostTags = strings.Split(*tags, ",")
	}

	exproject, err := c.ExportProject(lairPID)
	if err != nil {
		log.Fatalf("Fatal: Unable to export project. Error %s\n", err.Error())
	}

	project := &lair.Project{
		ID:   lairPID,
		Tool: tool,
		Commands: []lair.Command{lair.Command{
			Tool: tool,
		}},
	}

	for _, result := range recData.Hosts {
		found := false
		for i := range exproject.Hosts {
			h := exproject.Hosts[i]
			if result.IPAddress == h.IPv4 {
				exproject.Hosts[i].Hostnames = append(exproject.Hosts[i].Hostnames, result.Name)
				exproject.Hosts[i].LastModifiedBy = tool
				found = true
				if _, ok := tagSet[h.IPv4]; !ok {
					tagSet[h.IPv4] = true
					exproject.Hosts[i].Tags = append(exproject.Hosts[i].Tags, hostTags...)
				}
			}
		}
		if !found && result.IPAddress != "" {
			rNotFound[result.IPAddress] = append(rNotFound[result.IPAddress], result)
		}
	}

	for _, h := range exproject.Hosts {
		project.Hosts = append(project.Hosts, lair.Host{
			IPv4:           h.IPv4,
			LongIPv4Addr:   h.LongIPv4Addr,
			IsFlagged:      h.IsFlagged,
			LastModifiedBy: h.LastModifiedBy,
			MAC:            h.MAC,
			OS:             h.OS,
			Status:         h.Status,
			StatusMessage:  h.StatusMessage,
			Tags:           hostTags,
			Hostnames:      h.Hostnames,
		})
	}

	if *forceHosts {
		for ip, results := range rNotFound {
			hostnames := []string{}
			for _, r := range results {
				hostnames = append(hostnames, r.Name)
			}
			project.Hosts = append(project.Hosts, lair.Host{
				IPv4:      ip,
				Hostnames: hostnames,
			})
		}
	}

	for _, p := range recData.NetBlocks {
		nb := lair.Netblock{}
		nb.ProjectID = project.ID
		nb.MiscEmails = p.Email
		nb.CIDR = p.Netblock
		nb.Handle = p.OrgHandle
		project.Netblocks = append(project.Netblocks, nb)
	}

	for _, c := range recData.Contacts {
		per := lair.Person{}
		per.ProjectID = exproject.ID
		per.PrincipalName = c.Email
		per.FirstName = c.FirstName
		per.MiddleName = c.MiddleName
		per.LastName = c.LastName
		per.Emails = append(per.Emails, c.Email)
		per.Address = c.Region
		per.Department = c.Title
		project.People = append(project.People, per)
	}

	res, err := c.ImportProject(&client.DOptions{ForcePorts: *forcePorts}, project)

	if err != nil {
		log.Fatalf("Fatal: Unable to import project. Error %s\n", err)
	}

	defer res.Body.Close()
	droneRes := &client.Response{}
	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		log.Fatalf("Fatal: Error %s", err.Error())
	}

	if err := json.Unmarshal(body, droneRes); err != nil {
		log.Fatalf("Fatal: Could not unmarshal JSON. Error %s\n", err.Error())
	}

	if droneRes.Status == "Error" {
		log.Fatalf("Fatal: Import failed. Error %s\n", droneRes.Message)
	}

	if len(rNotFound) > 0 {
		if *forceHosts {
			log.Println("Info: The following hosts had hostnames and were forced to import into lair")
		} else {
			log.Println("Info: The following hosts had hostnames but could not be imported because they do not exist in lair")
		}
	}

	for k := range rNotFound {
		fmt.Println(k)
	}

	log.Println("Success: Operation completed successfully")
}
