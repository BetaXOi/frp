// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"sync"
	"time"

	"github.com/fatedier/frp/models/config"
	"github.com/fatedier/frp/utils/log"
	"github.com/fatedier/frp/utils/metric"
)

const (
	ReserveDays = 7
)

var globalStats *ServerStatistics

type ServerStatistics struct {
	TotalTrafficIn  metric.DateCounter
	TotalTrafficOut metric.DateCounter
	CurConns        metric.Counter

	// counter for clients
	ClientCounts metric.Counter

	// counter for proxy types
	ProxyTypeCounts map[string]metric.Counter

	// statistics for different runIds
	// key is runId
	ClientStatistics map[string]*ClientStatistics

	mu sync.Mutex
}

type ClientStatistics struct {
	RunId      string
	ClientAddr string
	Direct     bool

	// statistics for different proxies
	// key is proxy name
	ProxyStatistics map[string]*ProxyStatistics
}

type ProxyStatistics struct {
	Name          string
	ProxyType     string
	TrafficIn     metric.DateCounter
	TrafficOut    metric.DateCounter
	CurConns      metric.Counter
	LastStartTime time.Time
	LastCloseTime time.Time
	RunId         string
}

func init() {
	globalStats = &ServerStatistics{
		TotalTrafficIn:  metric.NewDateCounter(ReserveDays),
		TotalTrafficOut: metric.NewDateCounter(ReserveDays),
		CurConns:        metric.NewCounter(),

		ClientCounts:    metric.NewCounter(),
		ProxyTypeCounts: make(map[string]metric.Counter),

		ClientStatistics: make(map[string]*ClientStatistics),
	}

	go func() {
		for {
			time.Sleep(12 * time.Hour)
			log.Debug("start to clear useless proxy statistics data...")
			StatsClearUselessInfo()
			log.Debug("finish to clear useless proxy statistics data")
		}
	}()
}

func StatsClearUselessInfo() {
	// To check if there are proxies that closed than 7 days and drop them.
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()
	for _, clientStats := range globalStats.ClientStatistics {
		for name, data := range clientStats.ProxyStatistics {
			if !data.LastCloseTime.IsZero() && time.Since(data.LastCloseTime) > time.Duration(7*24)*time.Hour {
				delete(clientStats.ProxyStatistics, name)
				log.Trace("clear proxy [%s]'s statistics data, lastCloseTime: [%s]", name, data.LastCloseTime.String())
			}
		}

		if len(clientStats.ProxyStatistics) == 0 {
			delete(globalStats.ClientStatistics, clientStats.RunId)
		}
	}
}

func StatsNewClient(ctl *Control) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.ClientCounts.Inc(1)
	}

	clientStats, ok := globalStats.ClientStatistics[ctl.runId]
	if !ok {
		clientStats = &ClientStatistics{
			RunId:  ctl.runId,
			Direct: false,
		}
		clientStats.ProxyStatistics = make(map[string]*ProxyStatistics)
		globalStats.ClientStatistics[ctl.runId] = clientStats
	}
	clientStats.ClientAddr = ctl.conn.RemoteAddr().String()
	if clientStats.ClientAddr == ctl.loginMsg.ClientAddr {
		clientStats.Direct = true
	}
}

func StatsCloseClient() {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.ClientCounts.Dec(1)
	}
}

func StatsNewProxy(runId string, name string, proxyType string) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.mu.Lock()
		defer globalStats.mu.Unlock()
		counter, ok := globalStats.ProxyTypeCounts[proxyType]
		if !ok {
			counter = metric.NewCounter()
		}
		counter.Inc(1)
		globalStats.ProxyTypeCounts[proxyType] = counter

		clientStats, ok := globalStats.ClientStatistics[runId]
		if !ok {
			clientStats = &ClientStatistics{
				RunId: runId,
			}
			clientStats.ProxyStatistics = make(map[string]*ProxyStatistics)
			globalStats.ClientStatistics[runId] = clientStats
		}

		proxyStats, ok := clientStats.ProxyStatistics[name]
		if !(ok && proxyStats.ProxyType == proxyType) {
			proxyStats = &ProxyStatistics{
				Name:       name,
				ProxyType:  proxyType,
				CurConns:   metric.NewCounter(),
				TrafficIn:  metric.NewDateCounter(ReserveDays),
				TrafficOut: metric.NewDateCounter(ReserveDays),
				RunId:      runId,
			}
			clientStats.ProxyStatistics[name] = proxyStats
		}
		proxyStats.LastStartTime = time.Now()
	}
}

func StatsCloseProxy(runId string, proxyName string, proxyType string) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.mu.Lock()
		defer globalStats.mu.Unlock()
		if counter, ok := globalStats.ProxyTypeCounts[proxyType]; ok {
			counter.Dec(1)
		}
		if clientStats, ok := globalStats.ClientStatistics[runId]; ok {
			if proxyStats, ok := clientStats.ProxyStatistics[proxyName]; ok {
				proxyStats.LastCloseTime = time.Now()
			}
		}
	}
}

func StatsOpenConnection(runId string, name string) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.CurConns.Inc(1)

		globalStats.mu.Lock()
		defer globalStats.mu.Unlock()
		clientStats, ok := globalStats.ClientStatistics[runId]
		if ok {
			proxyStats, ok := clientStats.ProxyStatistics[name]
			if ok {
				proxyStats.CurConns.Inc(1)
				clientStats.ProxyStatistics[name] = proxyStats
			}
		}
	}
}

func StatsCloseConnection(runId string, name string) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.CurConns.Dec(1)

		globalStats.mu.Lock()
		defer globalStats.mu.Unlock()
		clientStats, ok := globalStats.ClientStatistics[runId]
		if ok {
			proxyStats, ok := clientStats.ProxyStatistics[name]
			if ok {
				proxyStats.CurConns.Dec(1)
				clientStats.ProxyStatistics[name] = proxyStats
			}
		}
	}
}

func StatsAddTrafficIn(runId string, name string, trafficIn int64) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.TotalTrafficIn.Inc(trafficIn)

		globalStats.mu.Lock()
		defer globalStats.mu.Unlock()

		clientStats, ok := globalStats.ClientStatistics[runId]
		if ok {
			proxyStats, ok := clientStats.ProxyStatistics[name]
			if ok {
				proxyStats.TrafficIn.Inc(trafficIn)
				clientStats.ProxyStatistics[name] = proxyStats
			}
		}
	}
}

func StatsAddTrafficOut(runId string, name string, trafficOut int64) {
	if config.ServerCommonCfg.DashboardPort != 0 {
		globalStats.TotalTrafficOut.Inc(trafficOut)

		globalStats.mu.Lock()
		defer globalStats.mu.Unlock()

		clientStats, ok := globalStats.ClientStatistics[runId]
		if ok {
			proxyStats, ok := clientStats.ProxyStatistics[name]
			if ok {
				proxyStats.TrafficOut.Inc(trafficOut)
				clientStats.ProxyStatistics[name] = proxyStats
			}
		}
	}
}

// Functions for getting server stats.
type ServerStats struct {
	TotalTrafficIn  int64
	TotalTrafficOut int64
	CurConns        int64
	ClientCounts    int64
	ProxyTypeCounts map[string]int64
}

func StatsGetServer() *ServerStats {
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()
	s := &ServerStats{
		TotalTrafficIn:  globalStats.TotalTrafficIn.TodayCount(),
		TotalTrafficOut: globalStats.TotalTrafficOut.TodayCount(),
		CurConns:        globalStats.CurConns.Count(),
		ClientCounts:    globalStats.ClientCounts.Count(),
		ProxyTypeCounts: make(map[string]int64),
	}
	for k, v := range globalStats.ProxyTypeCounts {
		s.ProxyTypeCounts[k] = v.Count()
	}
	return s
}

type ProxyStats struct {
	Name            string
	Type            string
	TodayTrafficIn  int64
	TodayTrafficOut int64
	LastStartTime   string
	LastCloseTime   string
	CurConns        int64
	RunId           string
}

func StatsGetProxiesByType(proxyType string) []*ProxyStats {
	res := make([]*ProxyStats, 0)
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()

	for _, clientStats := range globalStats.ClientStatistics {
		for _, proxyStats := range clientStats.ProxyStatistics {
			if proxyStats.ProxyType != proxyType {
				continue
			}

			ps := &ProxyStats{
				Name:            proxyStats.Name,
				Type:            proxyStats.ProxyType,
				TodayTrafficIn:  proxyStats.TrafficIn.TodayCount(),
				TodayTrafficOut: proxyStats.TrafficOut.TodayCount(),
				CurConns:        proxyStats.CurConns.Count(),
				RunId:           proxyStats.RunId,
			}
			if !proxyStats.LastStartTime.IsZero() {
				ps.LastStartTime = proxyStats.LastStartTime.Format("01-02 15:04:05")
			}
			if !proxyStats.LastCloseTime.IsZero() {
				ps.LastCloseTime = proxyStats.LastCloseTime.Format("01-02 15:04:05")
			}
			res = append(res, ps)
		}
	}
	return res
}

type ProxyTrafficInfo struct {
	Name       string
	TrafficIn  []int64
	TrafficOut []int64
}

func StatsGetProxyTraffic(name string) (res *ProxyTrafficInfo) {
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()

	for _, clientStats := range globalStats.ClientStatistics {
		proxyStats, ok := clientStats.ProxyStatistics[name]
		if ok {
			res = &ProxyTrafficInfo{
				Name: name,
			}
			res.TrafficIn = proxyStats.TrafficIn.GetLastDaysCount(ReserveDays)
			res.TrafficOut = proxyStats.TrafficOut.GetLastDaysCount(ReserveDays)
		}
	}
	return
}

type ProxyClientStats struct {
	RunId      string
	ClientAddr string
	Direct     bool
	ProxyStats []*ProxyStats
}

func StatsGetProxyClients() []*ProxyClientStats {
	res := make([]*ProxyClientStats, 0)
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()

	for _, clientStats := range globalStats.ClientStatistics {
		client := &ProxyClientStats{
			RunId:      clientStats.RunId,
			ClientAddr: clientStats.ClientAddr,
			Direct:     clientStats.Direct,
		}
		client.ProxyStats = make([]*ProxyStats, 0)
		res = append(res, client)

		for name, proxyStats := range clientStats.ProxyStatistics {
			ps := &ProxyStats{
				Name:            name,
				Type:            proxyStats.ProxyType,
				TodayTrafficIn:  proxyStats.TrafficIn.TodayCount(),
				TodayTrafficOut: proxyStats.TrafficOut.TodayCount(),
				CurConns:        proxyStats.CurConns.Count(),
			}
			if !proxyStats.LastStartTime.IsZero() {
				ps.LastStartTime = proxyStats.LastStartTime.Format("01-02 15:04:05")
			}
			if !proxyStats.LastCloseTime.IsZero() {
				ps.LastCloseTime = proxyStats.LastCloseTime.Format("01-02 15:04:05")
			}
			client.ProxyStats = append(client.ProxyStats, ps)
		}
	}
	return res
}
