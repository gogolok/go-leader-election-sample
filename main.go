package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nu7hatch/gouuid"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/sigmon"

	"code.cloudfoundry.org/auctioneer"
	"code.cloudfoundry.org/cflager"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/consuladapter"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/localip"
	"code.cloudfoundry.org/locket"
)

const (
	serverProtocol = "http"
)

var listenAddr = flag.String(
	"listenAddr",
	"0.0.0.0:9016",
	"host:port to serve auction and LRP stop requests on",
)

var lockTTL = flag.Duration(
	"lockTTL",
	locket.LockTTL,
	"TTL for service lock",
)

var lockRetryInterval = flag.Duration(
	"lockRetryInterval",
	locket.RetryInterval,
	"interval to wait before retrying a failed lock acquisition",
)

var consulCluster = flag.String(
	"consulCluster",
	"",
	"comma-separated list of consul server addresses (ip:port)",
)

func main() {
	cflager.AddFlags(flag.CommandLine)
	flag.Parse()
	logger, _ := cflager.New("gogo")
	logger.Info("starting")

	port, err := strconv.Atoi(strings.Split(*listenAddr, ":")[1])
	if err != nil {
		logger.Fatal("invalid-port", err)
	}

	consulClient, err := consuladapter.NewClientFromUrl(*consulCluster)
	if err != nil {
		logger.Fatal("new-client-failed", err)
	}

	clock := clock.NewClock()
	auctioneerServiceClient := auctioneer.NewServiceClient(consulClient, clock)
	lockMaintainer := initializeLockMaintainer(logger, auctioneerServiceClient, port)
	worker := initializeWorker(logger)

	members := grouper.Members{
		{"lock-maintainer", lockMaintainer},
		{"worker", worker},
	}

	group := grouper.NewOrdered(os.Interrupt, members)

	monitor := ifrit.Invoke(sigmon.New(group))

	logger.Info("started")

	err = <-monitor.Wait()
	if err != nil {
		logger.Error("exited-with-failure", err)
		os.Exit(1)
	}

	logger.Info("exited")
}

func initializeLockMaintainer(logger lager.Logger, serviceClient auctioneer.ServiceClient, port int) ifrit.Runner {
	uuid, err := uuid.NewV4()
	if err != nil {
		logger.Fatal("Couldn't generate uuid", err)
	}

	localIP, err := localip.LocalIP()
	if err != nil {
		logger.Fatal("Couldn't determine local IP", err)
	}

	address := fmt.Sprintf("%s://%s:%d", serverProtocol, localIP, port)
	auctioneerPresence := auctioneer.NewPresence(uuid.String(), address)

	lockMaintainer, err := serviceClient.NewAuctioneerLockRunner(logger, auctioneerPresence, *lockRetryInterval, *lockTTL)
	if err != nil {
		logger.Fatal("Couldn't create lock maintainer", err)
	}

	return lockMaintainer
}

func initializeWorker(logger lager.Logger) ifrit.Runner {
	return NewWorker(logger)
}

type worker struct {
	logger lager.Logger
}

func NewWorker(logger lager.Logger) worker {
	return worker{
		logger: logger,
	}

}

func (w worker) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	logger := w.logger.Session("worker")
	logger.Info("starting")

	defer func() {
		logger.Info("done")
	}()

	clock := clock.NewClock()

	var c <-chan time.Time
	c = clock.NewTimer(5 * time.Second).C()

	counter := 0
	logger.Info("value ", lager.Data{"counter": counter})

	for {
		select {
		case sig := <-signals:
			logger.Info("shutting-down", lager.Data{"received-signal": sig})
			return nil
		case <-c:
			logger.Info("value ", lager.Data{"counter": counter})
			c = clock.NewTimer(5 * time.Second).C()
		default:
			counter += 1
		}
	}
}
