package consumer

import (
	"fmt"
	"github.com/Shopify/sarama"
	cluster "github.com/bsm/sarama-cluster"
	"go-ka/config"
	"go-ka/logic"
	"sync"
	"sync/atomic"
	"time"
)

type Consumer[V any] struct {
	config      config.ProcessorConfig[V]
	worker      *cluster.Consumer
	groupId     string
	topic       string
	live        int32
	concurrency int32
	mutex       *sync.Mutex /**guarantee atomic for consumer*/
	logic       logic.Logic[any]
}

type Process[V any] struct {
	configs   config.ProcessorConfigs[V]
	consumers map[string]*Consumer[V]
}

type ProcessImpl interface {
	Consume() int
}

func NewProcess[V any](cfgs *config.ProcessorConfigs[V]) *Process[V] {
	return &Process[V]{
		configs:   *cfgs,
		consumers: newConsumers[V](cfgs, cfgs.Zookeeper),
	}
}

func newConsumers[V any](cfgs *config.ProcessorConfigs[V], zkper []string) map[string]*Consumer[V] {

	var retv = make(map[string]*Consumer[V])

	for k, v := range cfgs.Processors {

		newConfig := &cluster.Config{
			Config: sarama.NewConfig(),
		}

		newConfig.Consumer.Return.Errors = true
		newConfig.Consumer.Fetch.Max = v.FetchSize
		newConfig.Consumer.MaxProcessingTime = time.Duration(v.PollTimeout * 1000 * 1000) //milli to nao

		c, err := cluster.NewConsumer([]string{v.BoostrapServer}, zkper, v.GroupId, []string{v.Topic}, newConfig)

		if err != nil {
			panic(err)
		}

		/*if err2 != nil {
			panic(err2)
		}*/

		csm := &Consumer[V]{
			config: v,
			//client:        &c,
			worker:  c,
			groupId: v.GroupId,
			//worker:      toMap(partitions), //all dead(nil) for init
			topic:       v.Topic,
			live:        0,
			concurrency: v.Concurrency,
			mutex:       &sync.Mutex{}, /**guarantee atomic for consumer*/
			logic:       logic.Logic[any](v.LogicContainer.Logic),
		}
		retv[k] = csm
	}

	return retv
}

func toMap(nums []int32) map[int32]*cluster.Consumer {
	var retv = make(map[int32]*cluster.Consumer)

	for _, v := range nums {
		retv[v] = nil
	}
	return retv
}

func (p *Process[V]) Consume() map[string]int32 {
	retv := make(map[string]int32)
	for _, v := range p.consumers {

		numToRevive := v.concurrency - v.live
		if numToRevive > 0 {
			retv["topic:"+v.topic+" group id : "+v.groupId] = numToRevive
		}

		for i := int32(0); i < numToRevive; i++ {
			{
				go func() {
					for {
						select {
						case msg1 := <-(*v.worker).Messages():
							res := (v.logic.Deserialize)(msg1.Value)
							atomic.AddInt32(&v.live, 1)

							err := v.logic.DoAction(res)
							if err != nil {
								fmt.Printf("%s", err)
							}

							err1 := (*v.worker).Commit()
							if err1 != nil {
								return
							}
						case msg1 := <-(*v.worker).Errors():
							fmt.Println("error", msg1)
							atomic.AddInt32(&v.live, -1)
							break
						}
					}
				}()

			}
		}

	}
	return retv

}
