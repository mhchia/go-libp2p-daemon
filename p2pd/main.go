package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/crypto"
	plaintext "github.com/libp2p/go-libp2p-core/sec/insecure"
	noise "github.com/libp2p/go-libp2p-noise"
	secio "github.com/libp2p/go-libp2p-secio"

	golog "github.com/ipfs/go-log"
	relay "github.com/libp2p/go-libp2p-circuit"
	connmgr "github.com/libp2p/go-libp2p-connmgr"
	p2pd "github.com/libp2p/go-libp2p-daemon"
	config "github.com/libp2p/go-libp2p-daemon/config"
	ps "github.com/libp2p/go-libp2p-pubsub"
	quic "github.com/libp2p/go-libp2p-quic-transport"
	identify "github.com/libp2p/go-libp2p/p2p/protocol/identify"
	multiaddr "github.com/multiformats/go-multiaddr"
	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"
	gologging "github.com/whyrusleeping/go-logging"

	_ "net/http/pprof"
)

func pprofHTTP(port int) {
	listen := func(p int) error {
		addr := fmt.Sprintf("localhost:%d", p)
		log.Printf("registering pprof debug http handler at: http://%s/debug/pprof/\n", addr)
		switch err := http.ListenAndServe(addr, nil); err {
		case nil:
			// all good, server is running and exited normally.
			return nil
		case http.ErrServerClosed:
			// all good, server was shut down.
			return nil
		default:
			// error, try another port
			log.Printf("error registering pprof debug http handler at: %s: %s\n", addr, err)
			return err
		}
	}

	if port > 0 {
		// we have a user-assigned port.
		_ = listen(port)
		return
	}

	// we don't have a user assigned port, try sequentially to bind between [6060-7080]
	for i := 6060; i <= 7080; i++ {
		if listen(i) == nil {
			return
		}
	}
}

func main() {
	golog.SetAllLoggers(gologging.DEBUG) // Change to DEBUG for extra info

	identify.ClientVersion = "p2pd/0.1"

	security := flag.String("security", "", "security protocol used for secure channel")
	maddrString := flag.String("listen", "/unix/tmp/p2pd.sock", "daemon control listen multiaddr")
	quiet := flag.Bool("q", false, "be quiet")
	id := flag.String("id", "", "peer identity; private key file")
	bootstrap := flag.Bool("b", false, "connects to bootstrap peers and bootstraps the dht if enabled")
	bootstrapPeers := flag.String("bootstrapPeers", "", "comma separated list of bootstrap peers; defaults to the IPFS DHT peers")
	dht := flag.Bool("dht", false, "Enables the DHT in full node mode")
	dhtClient := flag.Bool("dhtClient", false, "Enables the DHT in client mode")
	connMgr := flag.Bool("connManager", false, "Enables the Connection Manager")
	connMgrLo := flag.Int("connLo", 256, "Connection Manager Low Water mark")
	connMgrHi := flag.Int("connHi", 512, "Connection Manager High Water mark")
	connMgrGrace := flag.Duration("connGrace", 120, "Connection Manager grace period (in seconds)")
	QUIC := flag.Bool("quic", false, "Enables the QUIC transport")
	natPortMap := flag.Bool("natPortMap", false, "Enables NAT port mapping")
	pubsub := flag.Bool("pubsub", false, "Enables pubsub")
	pubsubRouter := flag.String("pubsubRouter", "gossipsub", "Specifies the pubsub router implementation")
	pubsubSign := flag.Bool("pubsubSign", true, "Enables pubsub message signing")
	pubsubSignStrict := flag.Bool("pubsubSignStrict", true, "Enables or disables pubsub strict signature verification")
	gossipsubHeartbeatInterval := flag.Duration("gossipsubHeartbeatInterval", 0, "Specifies the gossipsub heartbeat interval")
	gossipsubHeartbeatInitialDelay := flag.Duration("gossipsubHeartbeatInitialDelay", 0, "Specifies the gossipsub initial heartbeat delay")
	relayEnabled := flag.Bool("relay", true, "Enables circuit relay")
	relayActive := flag.Bool("relayActive", false, "Enables active mode for relay")
	relayHop := flag.Bool("relayHop", false, "Enables hop for relay")
	relayDiscovery := flag.Bool("relayDiscovery", false, "Enables passive discovery for relay")
	autoRelay := flag.Bool("autoRelay", false, "Enables autorelay")
	autonat := flag.Bool("autonat", false, "Enables the AutoNAT service")
	hostAddrs := flag.String("hostAddrs", "", "comma separated list of multiaddrs the host should listen on")
	announceAddrs := flag.String("announceAddrs", "", "comma separated list of multiaddrs the host should announce to the network")
	noListen := flag.Bool("noListenAddrs", false, "sets the host to listen on no addresses")
	metricsAddr := flag.String("metricsAddr", "", "an address to bind the metrics handler to")
	configFilename := flag.String("f", "", "a file from which to read a json representation of the deamon config")
	configStdin := flag.Bool("i", false, "have the daemon read the json config from stdin")
	pprof := flag.Bool("pprof", false, "Enables the HTTP pprof handler, listening on the first port "+
		"available in the range [6060-7800], or on the user-provided port via -pprofPort")
	pprofPort := flag.Uint("pprofPort", 0, "Binds the HTTP pprof handler to a specific port; "+
		"has no effect unless the pprof option is enabled")

	flag.Parse()

	var c config.Config
	var opts []libp2p.Option

	if *configStdin {
		stdin := bufio.NewReader(os.Stdin)
		body, err := ioutil.ReadAll(stdin)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(body, &c); err != nil {
			log.Fatal(err)
		}
	} else if *configFilename != "" {
		body, err := ioutil.ReadFile(*configFilename)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(body, &c); err != nil {
			log.Fatal(err)
		}
	} else {
		c = config.NewDefaultConfig()
	}

	maddr, err := multiaddr.NewMultiaddr(*maddrString)
	if err != nil {
		log.Fatal(err)
	}
	c.ListenAddr = config.JSONMaddr{maddr}

	if *id != "" {
		c.ID = *id
	}

	if *hostAddrs != "" {
		addrStrings := strings.Split(*hostAddrs, ",")
		ha := make([]multiaddr.Multiaddr, len(addrStrings))
		for i, s := range addrStrings {
			ma, err := multiaddr.NewMultiaddr(s)
			if err != nil {
				log.Fatal(err)
			}
			(ha)[i] = ma
		}
		c.HostAddresses = ha
	}

	if *announceAddrs != "" {
		addrStrings := strings.Split(*announceAddrs, ",")
		ha := make([]multiaddr.Multiaddr, len(addrStrings))
		for i, s := range addrStrings {
			ma, err := multiaddr.NewMultiaddr(s)
			if err != nil {
				log.Fatal(err)
			}
			(ha)[i] = ma
		}
		c.AnnounceAddresses = ha
	}

	if *connMgr {
		c.ConnectionManager.Enabled = true
		c.ConnectionManager.GracePeriod = *connMgrGrace
		c.ConnectionManager.HighWaterMark = *connMgrHi
		c.ConnectionManager.LowWaterMark = *connMgrLo
	}

	if *QUIC {
		c.QUIC = true
	}

	if *natPortMap {
		c.NatPortMap = true
	}

	if *relayEnabled {
		c.Relay.Enabled = true
		if *relayActive {
			c.Relay.Active = true
		}
		if *relayHop {
			c.Relay.Hop = true
		}
		if *relayDiscovery {
			c.Relay.Discovery = true
		}
	}

	if *autoRelay {
		c.Relay.Auto = true
	}

	if *noListen {
		c.NoListen = true
	}

	if *autonat {
		c.AutoNat = true
	}

	if *pubsub {
		c.PubSub.Enabled = true
		c.PubSub.Router = *pubsubRouter
		c.PubSub.Sign = *pubsubSign
		c.PubSub.SignStrict = *pubsubSignStrict
		if *gossipsubHeartbeatInterval > 0 {
			c.PubSub.GossipSubHeartbeat.Interval = *gossipsubHeartbeatInterval
		}
		if *gossipsubHeartbeatInitialDelay > 0 {
			c.PubSub.GossipSubHeartbeat.InitialDelay = *gossipsubHeartbeatInitialDelay
		}
	}

	if *bootstrapPeers != "" {
		addrStrings := strings.Split(*bootstrapPeers, ",")
		bps := make([]multiaddr.Multiaddr, len(addrStrings))
		for i, s := range addrStrings {
			ma, err := multiaddr.NewMultiaddr(s)
			if err != nil {
				log.Fatal(err)
			}
			(bps)[i] = ma
		}
		c.Bootstrap.Peers = bps
	}

	if *bootstrap {
		c.Bootstrap.Enabled = true
	}

	if *quiet {
		c.Quiet = true
	}

	if *metricsAddr != "" {
		c.MetricsAddress = *metricsAddr
	}

	if *dht {
		c.DHT.Mode = config.DHTFullMode
	} else if *dhtClient {
		c.DHT.Mode = config.DHTClientMode
	}

	if *pprof {
		c.PProf.Enabled = true
		if pprofPort != nil {
			c.PProf.Port = *pprofPort
		}
	}

	if err := c.Validate(); err != nil {
		log.Fatal(err)
	}

	if c.PProf.Enabled {
		// an invalid port number will fail within the function.
		go pprofHTTP(int(c.PProf.Port))
	}

	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, rand.Reader)
	opts = append(opts, libp2p.Identity(priv))

	if len(c.HostAddresses) > 0 {
		opts = append(opts, libp2p.ListenAddrs(c.HostAddresses...))
	}

	if len(c.AnnounceAddresses) > 0 {
		opts = append(opts, libp2p.AddrsFactory(func([]multiaddr.Multiaddr) []multiaddr.Multiaddr {
			return c.AnnounceAddresses
		}))
	}

	if c.ConnectionManager.Enabled {
		cm := connmgr.NewConnManager(c.ConnectionManager.LowWaterMark,
			c.ConnectionManager.HighWaterMark,
			c.ConnectionManager.GracePeriod)
		opts = append(opts, libp2p.ConnectionManager(cm))
	}

	protocolID := *security
	if protocolID == plaintext.ID {
		opts = append(opts, libp2p.NoSecurity)
	} else if protocolID == noise.ID {
		tpt, err := noise.New(priv, noise.NoiseKeyPair(nil))
		if err != nil {
			log.Fatal("failed to initialize noise key pair")
		}
		opts = append(opts, libp2p.Security(protocolID, tpt))
	} else if protocolID == secio.ID {
		tpt, err := secio.New(priv)
		if err != nil {
			log.Fatal("failed to initialize noise secio pair")
		}
		opts = append(opts, libp2p.Security(protocolID, tpt))
	} else {
		log.Fatalf("security protocolID '%s' is not supported", protocolID)
	}

	if c.QUIC {
		opts = append(opts,
			libp2p.DefaultTransports,
			libp2p.Transport(quic.NewTransport),
		)
		if len(c.HostAddresses) == 0 {
			log.Fatal("if we explicitly specify a transport, we must also explicitly specify the listen addrs")
		}
	}

	if c.NatPortMap {
		opts = append(opts, libp2p.NATPortMap())
	}

	if c.Relay.Enabled {
		var relayOpts []relay.RelayOpt
		if c.Relay.Active {
			relayOpts = append(relayOpts, relay.OptActive)
		}
		if c.Relay.Hop {
			relayOpts = append(relayOpts, relay.OptHop)
		}
		if c.Relay.Discovery {
			relayOpts = append(relayOpts, relay.OptDiscovery)
		}
		opts = append(opts, libp2p.EnableRelay(relayOpts...))

		if c.Relay.Auto {
			opts = append(opts, libp2p.EnableAutoRelay())
		}
	}

	if c.NoListen {
		opts = append(opts, libp2p.NoListenAddrs)
	}

	// start daemon
	d, err := p2pd.NewDaemon(context.Background(), &c.ListenAddr, c.DHT.Mode, opts...)
	if err != nil {
		log.Fatal(err)
	}

	if c.AutoNat {
		var opts []libp2p.Option
		// allow the AutoNAT service to dial back quic addrs.
		if c.QUIC {
			opts = append(opts,
				libp2p.DefaultTransports,
				libp2p.Transport(quic.NewTransport),
			)
		}
		err := d.EnableAutoNAT(opts...)
		if err != nil {
			log.Fatal(err)
		}
	}

	if c.PubSub.Enabled {
		if c.PubSub.GossipSubHeartbeat.Interval > 0 {
			ps.GossipSubHeartbeatInterval = c.PubSub.GossipSubHeartbeat.Interval
		}
		if c.PubSub.GossipSubHeartbeat.InitialDelay > 0 {
			ps.GossipSubHeartbeatInitialDelay = c.PubSub.GossipSubHeartbeat.InitialDelay
		}

		err = d.EnablePubsub(c.PubSub.Router, c.PubSub.Sign, c.PubSub.SignStrict)
		if err != nil {
			log.Fatal(err)
		}
	}

	if len(c.Bootstrap.Peers) > 0 {
		p2pd.BootstrapPeers = c.Bootstrap.Peers
	}

	if c.Bootstrap.Enabled {
		err = d.Bootstrap()
		if err != nil {
			log.Fatal(err)
		}
	}

	if !c.Quiet {
		fmt.Printf("Control socket: %s\n", c.ListenAddr.String())
		fmt.Printf("Peer ID: %s\n", d.ID().Pretty())
		fmt.Printf("Peer Addrs:\n")
		for _, addr := range d.Addrs() {
			fmt.Printf("%s\n", addr.String())
		}
		if c.Bootstrap.Enabled && len(c.Bootstrap.Peers) > 0 {
			fmt.Printf("Bootstrap peers:\n")
			for _, p := range p2pd.BootstrapPeers {
				fmt.Printf("%s\n", p)
			}
		}
	}

	if c.MetricsAddress != "" {
		http.Handle("/metrics", promhttp.Handler())
		go func() { log.Println(http.ListenAndServe(c.MetricsAddress, nil)) }()
	}

	select {}
}
