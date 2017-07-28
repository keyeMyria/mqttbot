package bot

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/eclipse/paho.mqtt.golang"
	"github.com/garyburd/redigo/redis"
	"github.com/spf13/viper"
	"github.com/topfreegames/mqttbot/logger"
	"github.com/topfreegames/mqttbot/modules"
	"github.com/topfreegames/mqttbot/mqttclient"
	"github.com/topfreegames/mqttbot/plugins"
)

// PluginMapping defines the plugin to listen to given patterns
type PluginMapping struct {
	Plugin         string
	MessagePattern string
}

// Subscription defines the plugin mappings to a given topic
type Subscription struct {
	Topic          string
	Qos            int
	PluginMappings []*PluginMapping
}

// MQTTBot defines the bot, it contains plugins, subscriptions and a client
type MQTTBot struct {
	Plugins       *plugins.Plugins
	Subscriptions []*Subscription
	Client        *mqttclient.MQTTClient
	Config        *viper.Viper
}

var mqttBot *MQTTBot
var once sync.Once

var h mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	for _, subscription := range mqttBot.Subscriptions {
		if RouteIncludesTopic(strings.Split(subscription.Topic, "/"), strings.Split(msg.Topic(), "/")) {
			for _, pluginMapping := range subscription.PluginMappings {
				match, _ := regexp.Match(pluginMapping.MessagePattern, msg.Payload())
				if match {
					mqttBot.Plugins.ExecutePlugin(string(msg.Payload()[:]), msg.Topic(), pluginMapping.Plugin)
				}
			}
		}
	}
}

//ResetMQTTBot clears once and mqttbot instance
func ResetMQTTBot() {
	once = sync.Once{}
	mqttBot = nil
}

// GetMQTTBot returns a initialized mqtt bot
func GetMQTTBot() *MQTTBot {
	once.Do(func() {
		addCredentialsToRedis()
		mqttBot = &MQTTBot{}
		mqttBot.Client = mqttclient.GetMQTTClient(onClientConnectHandler)
		mqttBot.setupPlugins()
	})
	return mqttBot
}

func (b *MQTTBot) setupPlugins() {
	b.Plugins = plugins.GetPlugins()
	b.Plugins.SetupPlugins()
}

var onClientConnectHandler = func(client mqtt.Client) {
	mqttBot.StartBot()
}

// StartBot starts the bot, it subscribes the bot to the topics defined in the
// configuration file
func (b *MQTTBot) StartBot() {
	subscriptions := viper.Get("mqttserver.subscriptionRequests").([]interface{})
	client := b.Client.MQTTClient
	fmt.Println(12)
	b.Subscriptions = []*Subscription{}
	for _, s := range subscriptions {
		sMap := s.(map[interface{}]interface{})
		qos := sMap[string("qos")].(int)
		topic := sMap[string("topic")].(string)
		pluginMapping := sMap[string("plugins")].([]interface{})
		subscriptionNow := &Subscription{
			Topic:          topic,
			Qos:            qos,
			PluginMappings: []*PluginMapping{},
		}
		for _, p := range pluginMapping {
			pMap := p.(map[interface{}]interface{})
			subscriptionNow.PluginMappings = append(subscriptionNow.PluginMappings, &PluginMapping{
				Plugin:         pMap[string("plugin")].(string),
				MessagePattern: pMap[string("messagePattern")].(string),
			})
		}

		for i := 0; i < 10; i++ {
			if tokenUnsubscribe := client.Unsubscribe(topic); tokenUnsubscribe.Wait() && tokenUnsubscribe.Error() != nil {
				logger.Logger.Info(tokenUnsubscribe.Error())
			}
		}

		if token := client.Subscribe(topic, uint8(qos), h); token.Wait() && token.Error() != nil {
			logger.Logger.Fatal(token.Error())
		}
		logger.Logger.Debug(fmt.Sprintf("Subscribed to %s", topic))
		b.Subscriptions = append(b.Subscriptions, subscriptionNow)
	}
}

func addCredentialsToRedis() {
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.maxPoolSize", 10)
	user := viper.GetString("mqttserver.user")
	pass := viper.GetString("mqttserver.pass")
	hash := modules.GenHash(pass)
	redisHost := viper.GetString("redis.host")
	redisPort := viper.GetInt("redis.port")
	redisPass := viper.GetString("redis.password")
	logger.Logger.Info(fmt.Sprintf("Connecting to redis at %s:%d", redisHost, redisPort))
	conn, err := redis.Dial("tcp", fmt.Sprintf("%s:%d", redisHost, redisPort),
		redis.DialPassword(redisPass))
	if err != nil {
		conn, err = redis.Dial("tcp", fmt.Sprintf("%s:%d", redisHost, redisPort))
		if err != nil {
			logger.Logger.Fatal(fmt.Sprintf("Error connecting to Redis: %v", err))
			return
		}
	}
	defer conn.Close()
	if _, err = conn.Do("SET", user, hash); err != nil {
		logger.Logger.Error(fmt.Sprintf("Error adding pass to redis: %v", err))
	}
}
