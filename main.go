package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"strings"
	"time"
	"path/filepath"
    "bufio"
	"os"

	config "github.com/ipfs/go-ipfs-config"
	core "github.com/ipfs/go-ipfs/core"
	libp2p "github.com/ipfs/go-ipfs/core/node/libp2p"
	fsrepo "github.com/ipfs/go-ipfs/repo/fsrepo"
	peerstore "github.com/libp2p/go-libp2p-core/peer"

	// "github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-files"
	"github.com/ipfs/go-ipfs/core/coreapi"

	// "github.com/ipfs/go-ipfs/core/coreunix"
	// "github.com/ipfs/interface-go-ipfs-core/options"
	icorepath "github.com/ipfs/interface-go-ipfs-core/path"
	"github.com/ipfs/go-ipfs/plugin/loader"

	peer "github.com/libp2p/go-libp2p-core/peer"
	ping "github.com/libp2p/go-libp2p/p2p/protocol/ping"
	ma "github.com/multiformats/go-multiaddr"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println("Spawning ephemeral ipfs node")
	node, err := spawnEphemeral(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to spawn ephemeral node: %s", err))
	}

	// PEER DYNAMICS
	f, err := os.OpenFile("peers-sec.csv", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	nodeCoreApi, _ := coreapi.NewCoreAPI(node)
	for i := 0; i < 60; i++ {
		peers, _ := nodeCoreApi.Swarm().Peers(ctx)
		f.WriteString(fmt.Sprintf("%d,%d\n", i, len(peers)))
		fmt.Println(i, "sec ==>", len(peers), "peers")
		// wait to discover more neighbors
		time.Sleep(1 * time.Second)
	}
	f.Close()

	// return

	// GO THROUGH NEIGHBORS AND PING
	peerCount := 0
	pingTime := 0
	peers, _ := nodeCoreApi.Swarm().Peers(ctx)
	for _, peer := range peers {
		pings := ping.Ping(ctx, node.PeerHost, peer.ID())
		for i := 1; i <= 10; i++ {
			response := <- pings
			if response.Error == nil {
				pingTime += int(response.RTT.Milliseconds())
				peerCount += 1
			}
		}
	}
	fmt.Println(peerCount, "peers ==> avg time", pingTime / peerCount, "msec per peer")

	// RUN GET QUERIES
	f, _ = os.OpenFile("query.csv", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	defer f.Close()
	file, _ := os.Open("input.txt")
	defer file.Close()
	scanner := bufio.NewScanner(file)
	total := 0
	i := 0
	for scanner.Scan() {
		start := time.Now()
		GetFile(ctx, node, scanner.Text())
		elapsed := time.Since(start)
		i += 1
		// fmt.Println(i)
		total += int(elapsed.Milliseconds())
		if i == 10 || i == 20 || i == 40 {
			f.WriteString(fmt.Sprintf("%d,%d\n", i, total))
			fmt.Println(i, "get requests ==> time", total, "msec")
		}
	} 

	// ch := make(chan os.Signal, 1)
	// signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	// <-ch
	// fmt.Println("Received signal, shutting down...")
}

func setupPlugins(externalPluginsPath string) error {
	// Load any external plugins if available on externalPluginsPath
	plugins, err := loader.NewPluginLoader(filepath.Join(externalPluginsPath, "plugins"))
	if err != nil {
		return fmt.Errorf("error loading plugins: %s", err)
	}

	// Load preloaded and external plugins
	if err := plugins.Initialize(); err != nil {
		return fmt.Errorf("error initializing plugins: %s", err)
	}

	if err := plugins.Inject(); err != nil {
		return fmt.Errorf("error initializing plugins: %s", err)
	}

	return nil
}

func createTempRepo(ctx context.Context) (string, error) {
	repoPath, err := ioutil.TempDir("", "ipfs-shell")
	if err != nil {
		return "", fmt.Errorf("failed to get temp dir: %s", err)
	}
	fmt.Println(repoPath)
	// Create a config with default options and a 2048 bit key
	cfg, err := config.Init(ioutil.Discard, 2048)
	if err != nil {
		return "", err
	}

	// Create the repo with the config
	err = fsrepo.Init(repoPath, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to init ephemeral node: %s", err)
	}

	return repoPath, nil
}

// Spawns a node to be used just for this run (i.e. creates a tmp repo)
func spawnEphemeral(ctx context.Context) (*core.IpfsNode, error) {
	if err := setupPlugins(""); err != nil {
		return nil, err
	}

	// Create a Temporary Repo
	repoPath, err := createTempRepo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp repo: %s", err)
	}

	// Spawning an ephemeral IPFS node
	return createNode(ctx, repoPath)
}

// Creates an IPFS node and returns its coreAPI
func createNode(ctx context.Context, repoPath string) (*core.IpfsNode, error) {
	// Open the repo
	repo, err := fsrepo.Open(repoPath)
	if err != nil {
		return nil, err
	}

	// Construct the node
	nodeOptions := &core.BuildCfg{
		Online:  true,
		Routing: libp2p.DHTOption, // This option sets the node to be a full DHT node (both fetching and storing DHT Records)
		// Routing: libp2p.DHTClientOption, // This option sets the node to be a client DHT node (only fetching records)
		Repo: repo,
	}

	node, err := core.NewNode(ctx, nodeOptions)
	if err != nil {
		return nil, err
	}

	peer := node.PeerHost

	peerInfo := peerstore.AddrInfo{
		ID:    peer.ID(),
		Addrs: peer.Addrs(),
	}
	addrs, err := peerstore.AddrInfoToP2pAddrs(&peerInfo)
	fmt.Println("libp2p node address:", addrs)

	// Attach the Core API to the constructed node
	return node, nil
}

// Spawns a node on the default repo location, if the repo exists
func spawnDefault(ctx context.Context) (*core.IpfsNode, error) {
	defaultPath, err := config.PathRoot()
	if err != nil {
		// shouldn't be possible
		return nil, err
	}

	if err := setupPlugins(defaultPath); err != nil {
		return nil, err

	}

	return createNode(ctx, defaultPath)
}

// Get a file from ipfs
func GetFile(ctx context.Context, node *core.IpfsNode, fileCid string) (files.Node, error) {
	path := icorepath.New(fileCid)
	nodeCoreApi, err := coreapi.NewCoreAPI(node)
	if err != nil {
		fmt.Println("Cannot get the core API", err)
		return nil, err
	}
	rootNode, err := nodeCoreApi.Unixfs().Get(ctx, path)
	if err != nil {
		fmt.Println("Could not get file", err)
		return nil, err
	}
	return rootNode, nil
}

func parsePid(pid string) (peer.ID, error) {
	// Multiaddr
	if strings.HasPrefix(pid, "/") {
		maddr, err := ma.NewMultiaddr(pid)
		if err != nil {
			return "", err
		}
		_, id := peer.SplitAddr(maddr)
		if id == "" {
			return "", peer.ErrInvalidAddr
		}
		return id, nil
	}
	// Raw peer ID
	p, err := peer.Decode(pid)
	return p, err
}