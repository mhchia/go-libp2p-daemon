package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	libp2p "github.com/libp2p/go-libp2p"
	p2pd "github.com/libp2p/go-libp2p-daemon"
	identify "github.com/libp2p/go-libp2p/p2p/protocol/identify"
)

func main() {
	identify.ClientVersion = "p2pd/0.1"

	sock := flag.String("sock", "/tmp/p2pd.sock", "daemon control socket path")
	quiet := flag.Bool("q", false, "be quiet")
	id := flag.String("id", "", "peer identity; private key file")
	bootstrap := flag.Bool("b", false, "connects to bootstrap peers and bootstraps the dht if enabled")
	bootstrapPeers := flag.String("bootstrapPeers", "", "comma separated list of bootstrap peers; defaults to the IPFS DHT peers")
	dht := flag.Bool("dht", false, "Enables the DHT in full node mode")
	dhtClient := flag.Bool("dhtClient", false, "Enables the DHT in client mode")
	flag.Parse()

	var opts []libp2p.Option

	if *id != "" {
		key, err := p2pd.ReadIdentity(*id)
		if err != nil {
			log.Fatal(err)
		}

		opts = append(opts, libp2p.Identity(key))
	}

	d, err := p2pd.NewDaemon(context.Background(), *sock, opts...)
	if err != nil {
		log.Fatal(err)
	}

	if *dht || *dhtClient {
		err = d.EnableDHT(*dhtClient)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *bootstrapPeers != "" {
		p2pd.BootstrapPeers = strings.Split(*bootstrapPeers, ",")
	}

	if *bootstrap {
		err = d.Bootstrap()
		if err != nil {
			log.Fatal(err)
		}
	}

	if !*quiet {
		fmt.Printf("Control socket: %s\n", *sock)
		fmt.Printf("Peer ID: %s\n", d.ID().Pretty())
		fmt.Printf("Peer Addrs:\n")
		for _, addr := range d.Addrs() {
			fmt.Printf("%s\n", addr.String())
		}
		if *bootstrap && *bootstrapPeers != "" {
			fmt.Printf("Bootstrap peers:\n")
			for _, p := range p2pd.BootstrapPeers {
				fmt.Printf("%s\n", p)
			}
		}
	}

	select {}
}
