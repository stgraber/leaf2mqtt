package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/joeshaw/leaf"
	"github.com/eclipse/paho.mqtt.golang"
	"golang.org/x/sys/unix"
)

var (
	sessionLock sync.Mutex

	carInfo *leaf.VehicleInfo

	carBattery *leaf.BatteryRecords
	carBatteryLock sync.Mutex

	carTemperature *leaf.TemperatureRecords
	carTemperatureLock sync.Mutex

	carLocation *leaf.Location
	carLocationLock sync.Mutex

	carHVAC bool
	carHVACExpiry time.Time
	carHVACLock sync.Mutex
	carHVACReady bool

	mqttBinaryTopic string
	mqttSensorTopic string
	mqttSwitchTopic string
	mqttTrackerTopic string
)

func main() {
	log.SetOutput(os.Stdout)

	err := run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Setup error handling for background Go routines.
	chError := make(chan error, 0)

	// Setup MQTT connection details.
	mqttOpts := mqtt.NewClientOptions()
	mqttOpts.AddBroker(os.Getenv("MQTT_HOST"))
	mqttOpts.SetClientID("leaf2mqtt")
	mqttOpts.SetUsername(os.Getenv("MQTT_USERNAME"))
	mqttOpts.SetPassword(os.Getenv("MQTT_PASSWORD"))
	mqttOpts.SetAutoReconnect(true)
	mqttOpts.SetCleanSession(false)
	mqttSession := mqtt.NewClient(mqttOpts)
	if token := mqttSession.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Setup Nissan connection details.
	leafSession := &leaf.Session{
		Username: os.Getenv("LEAF_USERNAME"),
		Password: os.Getenv("LEAF_PASSWORD"),
		Country:  os.Getenv("LEAF_COUNTRY"),
	}

	// Login.
	info, _, _, err := leafSession.Login()
	if err != nil {
		return err
	}
	carInfo = info
	mqttBinaryTopic = fmt.Sprintf("homeassistant/binary_sensor/leaf2mqtt_%s", carInfo.VIN)
	mqttTrackerTopic = fmt.Sprintf("homeassistant/device_tracker/leaf2mqtt_%s", carInfo.VIN)
	mqttSensorTopic = fmt.Sprintf("homeassistant/sensor/leaf2mqtt_%s", carInfo.VIN)
	mqttSwitchTopic = fmt.Sprintf("homeassistant/switch/leaf2mqtt_%s", carInfo.VIN)

	// Create the MQTT location topics.
	data := fmt.Sprintf(`{
    "name": "%s (location)",
    "state_topic": "%s/location/state",
    "payload_home": "home",
    "payload_not_home": "not_home",
    "json_attributes_topic": "%s/location/attributes",
    "icon": "mdi:car",
    "source_type": "gps"
}`, carInfo.VIN, mqttTrackerTopic, mqttTrackerTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/location/config", mqttTrackerTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	if token := mqttSession.Publish(fmt.Sprintf("%s/location/state", mqttTrackerTopic), 0, true, "not_home"); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Create the MQTT battery topics.
	data = fmt.Sprintf(`{
    "name": "%s (charge)",
    "state_topic": "%s/charge/state",
    "device_class": "battery",
    "unit_of_measurement": "%%"
}`, carInfo.VIN, mqttSensorTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/charge/config", mqttSensorTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	data = fmt.Sprintf(`{
    "name": "%s (plugged)",
    "state_topic": "%s/plugged/state",
    "payload_on": "on",
    "payload_off": "off",
    "device_class": "plug"
}`, carInfo.VIN, mqttBinaryTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/plugged/config", mqttBinaryTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	data = fmt.Sprintf(`{
    "name": "%s (charging)",
    "state_topic": "%s/charging/state",
    "payload_on": "on",
    "payload_off": "off",
    "device_class": "battery_charging"
}`, carInfo.VIN, mqttBinaryTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/charging/config", mqttBinaryTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Create the MQTT range topics
	data = fmt.Sprintf(`{
    "name": "%s (range with AC)",
    "state_topic": "%s/range_ac/state",
    "unit_of_measurement": "km"
}`, carInfo.VIN, mqttSensorTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/range_ac/config", mqttSensorTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	data = fmt.Sprintf(`{
    "name": "%s (range without AC)",
    "state_topic": "%s/range_noac/state",
    "unit_of_measurement": "km"
}`, carInfo.VIN, mqttSensorTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/range_noac/config", mqttSensorTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Create the MQTT temperature topics.
	data = fmt.Sprintf(`{
    "name": "%s (temperature)",
    "state_topic": "%s/temperature/state",
    "device_class": "temperature",
    "unit_of_measurement": "C"
}`, carInfo.VIN, mqttSensorTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/temperature/config", mqttSensorTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Create the MQTT climate topics.
	data = fmt.Sprintf(`{
    "name": "%s (climate)",
    "state_topic": "%s/climate/state",
    "command_topic": "%s/climate/set",
    "payload_on": "on",
    "payload_off": "off",
    "retain": true,
    "state_on": "on",
    "state_off": "off"
}`, carInfo.VIN, mqttSwitchTopic, mqttSwitchTopic)
	if token := mqttSession.Publish(fmt.Sprintf("%s/climate/config", mqttSwitchTopic), 0, true, data); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	if token := mqttSession.Publish(fmt.Sprintf("%s/climate/state", mqttSwitchTopic), 0, true, "off"); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	callback := func(client mqtt.Client, msg mqtt.Message) {
		mqttClimateSet(leafSession, client, msg, chError)
	}

	if token := mqttSession.Subscribe(fmt.Sprintf("%s/climate/set", mqttSwitchTopic), 0, callback); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	// Start the goroutines
	go updateBattery(leafSession, mqttSession, chError)
	go updateLocation(leafSession, mqttSession, chError)
	go printStatus(leafSession, mqttSession, chError)
	go signalHandling(leafSession, chError)

	return <-chError
}

func mqttClimateSet(session *leaf.Session, mqttSession mqtt.Client, msg mqtt.Message, chError chan error) {
	value := msg.Payload()

	if !carHVACReady {
		carHVACReady = true
		log.Printf("[climate] Skipping initial request")
		return
	}

	if string(value) == "on" {
		err := turnClimateOn(session)
		if err == nil {
			if token := mqttSession.Publish(fmt.Sprintf("%s/climate/state", mqttSwitchTopic), 0, true, "on"); token.Wait() && token.Error() != nil {
				log.Printf("[climate] Failed to update MQTT: %v", token.Error())
				return
			}
		}
	} else if string(value) == "off" {
		err := turnClimateOff(session)
		if err == nil {
			if token := mqttSession.Publish(fmt.Sprintf("%s/climate/state", mqttSwitchTopic), 0, true, "off"); token.Wait() && token.Error() != nil {
				log.Printf("[climate] Failed to update MQTT: %v", token.Error())
				return
			}
		}
	} else {
		log.Printf("[climate] Invalid climate state: %v", value)
	}
}

func turnClimateOn(session *leaf.Session) error {
	log.Printf("[climate] Turning HVAC on")
	carHVACLock.Lock()
	defer carHVACLock.Unlock()

	if carHVAC {
		// Reset by attempting to turn off first.
		sessionLock.Lock()
		session.ClimateOff()
		sessionLock.Unlock()
	}

	sessionLock.Lock()
	err := session.ClimateOn()
	sessionLock.Unlock()
	if err != nil {
		log.Printf("[climate] Failed to turn on: %v", err)
		return err
	}

	carHVAC = true
	carHVACExpiry = time.Now().Add(15*time.Minute)

	log.Printf("[climate] Turned HVAC on")

	return nil
}

func turnClimateOff(session *leaf.Session) error {
	log.Printf("[climate] Turning HVAC off")
	carHVACLock.Lock()
	defer carHVACLock.Unlock()

	sessionLock.Lock()
	err := session.ClimateOff()
	sessionLock.Unlock()
	if err != nil {
		log.Printf("[climate] Failed to turn off: %v", err)
		return err
	}

	carHVAC = false
	log.Printf("[climate] Turned HVAC off")

	return nil
}

func signalHandling(session *leaf.Session, chError chan error) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, unix.SIGUSR1, unix.SIGUSR2)

	for {
		sig := <-sigs

		// Turn on the HVAC system.
		if sig == unix.SIGUSR1 {
			turnClimateOn(session)
		}

		// Turn off the HVAC system.
		if sig == unix.SIGUSR2 {
			turnClimateOff(session)
		}
	}
}

func printStatus(session *leaf.Session, mqttSession mqtt.Client, chError chan error) {
	printCurrent := func() {
		carBatteryLock.Lock()
		carLocationLock.Lock()
		carTemperatureLock.Lock()
		carHVACLock.Lock()
		defer carBatteryLock.Unlock()
		defer carLocationLock.Unlock()
		defer carTemperatureLock.Unlock()
		defer carHVACLock.Unlock()

		log.Printf("[info] Start of status update")
		log.Printf("Car information:")
		log.Printf("  Name: %s", carInfo.Nickname)
		log.Printf("  VIN: %s", carInfo.VIN)
		if carBattery != nil {
			hvacState := "off"
			if carHVAC {
				if carBattery.BatteryStatus.BatteryChargingStatus.IsCharging() {
					hvacState = "on"
				} else if carHVACExpiry.After(time.Now()) {
					hvacState = fmt.Sprintf("on (%s remaining)", time.Until(carHVACExpiry))
				}

				if hvacState == "off" {
					carHVAC = false
					if token := mqttSession.Publish(fmt.Sprintf("%s/climate/state", mqttSwitchTopic), 0, true, "off"); token.Wait() && token.Error() != nil {
						log.Printf("[climate] Failed to update MQTT: %v", token.Error())
					}
				}
			}

			log.Printf("Battery:")
			log.Printf("  Last update: %s", carBattery.LastUpdatedDateAndTime)
			log.Printf("  Plugged: %s", carBattery.PluginState)
			log.Printf("  Charging: %s", carBattery.BatteryStatus.BatteryChargingStatus)
			log.Printf("  Capacity: %d%%", carBattery.BatteryStatus.BatteryRemainingAmount)
			log.Printf("Range:")
			log.Printf("  With HVAC: %.0fkm", carBattery.CruisingRangeACOn/1000)
			log.Printf("  Without HVAC: %.0fkm", carBattery.CruisingRangeACOff/1000)
			log.Printf("Climate:")
			log.Printf("  HVAC: %s", hvacState)
			log.Printf("  Inside temperature: %sC", carTemperature.Temperature)
		}

		if carLocation != nil {
			log.Printf("Location:")
			log.Printf("  Last update: %s", carLocation.ReceivedDate)
			log.Printf("  Latitude: %s", carLocation.Latitude)
			log.Printf("  Longitude: %s", carLocation.Longitude)
		}
		log.Printf("[info] End of status update")
	}

	for {
		printCurrent()
		time.Sleep(2*time.Minute)
	}
}

func updateBattery(session *leaf.Session, mqttSession mqtt.Client, chError chan error) {
	for {
		sessionLock.Lock()
		retBattery, retTemperature, err := session.ChargingStatus()
		sessionLock.Unlock()
		if err != nil {
			log.Printf("[battery] Failed, retrying in 5 minutes: %v", err)
			sessionLock.Lock()
			for {
				_, _, _, err := session.Login()
				if err != nil {
					log.Printf("[battery] Failed login, retrying in 15s: %v", err)
					time.Sleep(15*time.Second)
					continue
				}

				break
			}
			sessionLock.Unlock()

			time.Sleep(5*time.Minute)
			continue
		}

		carBatteryLock.Lock()
		carBattery = retBattery
		carBatteryLock.Unlock()

		carTemperatureLock.Lock()
		carTemperature = retTemperature
		carTemperatureLock.Unlock()

		// MQTT charge update.
		if token := mqttSession.Publish(fmt.Sprintf("%s/charge/state", mqttSensorTopic), 0, true, fmt.Sprintf("%d", retBattery.BatteryStatus.BatteryRemainingAmount)); token.Wait() && token.Error() != nil {
			log.Printf("[battery] Failed to update MQTT: %v", token.Error())
		}

		// MQTT range update.
		if token := mqttSession.Publish(fmt.Sprintf("%s/range_ac/state", mqttSensorTopic), 0, true, fmt.Sprintf("%.0f", retBattery.CruisingRangeACOn/1000)); token.Wait() && token.Error() != nil {
			log.Printf("[battery] Failed to update MQTT: %v", token.Error())
		}

		if token := mqttSession.Publish(fmt.Sprintf("%s/range_noac/state", mqttSensorTopic), 0, true, fmt.Sprintf("%.0f", retBattery.CruisingRangeACOff/1000)); token.Wait() && token.Error() != nil {
			log.Printf("[battery] Failed to update MQTT: %v", token.Error())
		}

		// MQTT plugged in update.
		state := "off"
		if retBattery.PluginState == "CONNECTED" {
			state = "on"
		}
		if token := mqttSession.Publish(fmt.Sprintf("%s/plugged/state", mqttBinaryTopic), 0, true, state); token.Wait() && token.Error() != nil {
			log.Printf("[battery] Failed to update MQTT: %v", token.Error())
		}

		// MQTT charging update.
		state = "off"
		if retBattery.BatteryStatus.BatteryChargingStatus == "YES" {
			state = "on"
		}
		if token := mqttSession.Publish(fmt.Sprintf("%s/charging/state", mqttBinaryTopic), 0, true, state); token.Wait() && token.Error() != nil {
			log.Printf("[battery] Failed to update MQTT: %v", token.Error())
		}

		// MQTT temperature update.
		if token := mqttSession.Publish(fmt.Sprintf("%s/temperature/state", mqttSensorTopic), 0, true, retTemperature.Temperature); token.Wait() && token.Error() != nil {
			log.Printf("[battery] Failed to update MQTT: %v", token.Error())
		}

		if retBattery.BatteryStatus.BatteryChargingStatus.IsCharging() {
			log.Printf("[battery] Waiting for 5min (charging)")
			time.Sleep(5*time.Minute)
		} else if carHVAC {
			log.Printf("[battery] Waiting for 5min (heating up)")
			time.Sleep(5*time.Minute)
		} else {
			log.Printf("[battery] Waiting for 15min")
			time.Sleep(15*time.Minute)
		}
	}
}

func updateLocation(session *leaf.Session, mqttSession mqtt.Client, chError chan error) {
	for {
		sessionLock.Lock()
		ret, err := session.LocateVehicle()
		sessionLock.Unlock()
		if err != nil {
			log.Printf("[locate] Failed, retrying in 5 minutes: %v", err)
			sessionLock.Lock()
			for {
				_, _, _, err := session.Login()
				if err != nil {
					log.Printf("[battery] Failed login, retrying in 15s: %v", err)
					time.Sleep(15*time.Second)
					continue
				}

				break
			}
			sessionLock.Unlock()

			time.Sleep(5*time.Minute)
			continue
		}

		if ret.Latitude == "" || ret.Longitude == "" {
			log.Printf("[locate] Failed, retrying in 5 minutes: bad data received")
			sessionLock.Lock()
			for {
				_, _, _, err := session.Login()
				if err != nil {
					log.Printf("[battery] Failed login, retrying in 15s: %v", err)
					time.Sleep(15*time.Second)
					continue
				}

				break
			}
			sessionLock.Unlock()

			time.Sleep(5*time.Minute)
			continue
		}

		// MQTT location update.
		data := fmt.Sprintf(`{
    "latitude": %s,
    "longitude": %s,
    "gps_accuracy": 1.2
}`, ret.Latitude, ret.Longitude)
		if token := mqttSession.Publish(fmt.Sprintf("%s/location/attributes", mqttTrackerTopic), 0, true, data); token.Wait() && token.Error() != nil {
			log.Printf("[locate] Failed to update MQTT: %v", token.Error())
		}

		carLocationLock.Lock()
		moved := carLocation == nil || ret.Longitude != carLocation.Longitude || ret.Latitude != carLocation.Latitude
		carLocation = ret
		carLocationLock.Unlock()

		if moved {
			log.Printf("[locate] Waiting for 5min (moving)")
			time.Sleep(5*time.Minute)
		} else {
			log.Printf("[locate] Waiting for 15min")
			time.Sleep(15*time.Minute)
		}
	}
}
