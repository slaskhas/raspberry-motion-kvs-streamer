
package main

import (
	"context"
	"fmt"
	"os"
	"time"
	"reflect"
	"github.com/ziutek/glib"
	"github.com/ziutek/gst"
	"github.com/joho/godotenv"
	"github.com/at-wat/mqtt-go"
	"github.com/mitchellh/mapstructure"
	awsiotdev "github.com/seqsense/aws-iot-device-sdk-go/v6"
	"github.com/seqsense/aws-iot-device-sdk-go/v6/shadow"
	"github.com/stianeikeland/go-rpio/v4"
)



func checkElem(e *gst.Element, name string) {
	if e == nil {
		fmt.Fprintln(os.Stderr, "can't make element: ", name)
		os.Exit(1)
	}
}

// To play it using gst-launch-1.0
// gst-launch-1.0  v4l2src do-timestamp=TRUE device=/dev/video0 ! videoconvert ! video/x-raw,format=I420,width=320,height=240,framerate=10/1 ! omxh264enc control-rate=1 target-bitrate=2560000 periodicity-idr=45 inline-header=FALSE ! h264parse ! video/x-h264,stream-format=avc,alignment=au,width=320,height=240,framerate=10/1,profile=baseline ! kvssink stream-name="Claes_xxx"  access-key="AKIAxxx" secret-key="xxx" aws-region="us-west-2"

func setup_pipeline() (*gst.Pipeline) {

	src := gst.ElementFactoryMake("v4l2src", "VideoSrc")
	checkElem(src, "v4l2src")
	src.SetProperty("do-timestamp", true)
	src.SetProperty("device", "/dev/video0")

	conv := gst.ElementFactoryMake("videoconvert", "VideoConv")
	checkElem(conv, "videoconvert")

	omxh264enc := gst.ElementFactoryMake("omxh264enc", "OmxH264Enc")
	checkElem(omxh264enc, "omxh264enc")
	omxh264enc.SetProperty("control-rate",1)
	omxh264enc.SetProperty("target-bitrate",2560000)
	omxh264enc.SetProperty("periodicity-idr",45)
	omxh264enc.SetProperty("inline-header",false)

        h264parse := gst.ElementFactoryMake("h264parse", "H264Parse")
	checkElem(h264parse,"h264parse")

	//	fakesink := "fakesink" // To use during development
	//	fsink := gst.ElementFactoryMake(fakesink, "VideoSink")
	//	checkElem(fsink, fakesink)

	kvssink := gst.ElementFactoryMake("kvssink", "KVSSink") // make sure to build kvssink first
	checkElem(kvssink, "kvssink")
	kvssink.SetProperty("stream-name",os.Getenv("STREAM_NAME"))
	kvssink.SetProperty("access-key",os.Getenv("AWS_ACCESS_KEY_ID"))
	kvssink.SetProperty("secret-key",os.Getenv("AWS_SECRET_ACCESS_KEY"))
	kvssink.SetProperty("aws-region",os.Getenv("AWS_REGION"))
	kvssink.SetProperty("log-config",os.Getenv("KVSSINK_LOG_CONFIG"))

	pl := gst.NewPipeline("MyPipeline")
	pl.Add(src, conv, omxh264enc,h264parse, kvssink)

	filter := gst.NewCapsSimple(
		"video/x-raw",
		glib.Params{
		        "format":    "I420",
			"width":     int32(320),
			"height":    int32(240),
			"framerate": &gst.Fraction{10, 1},
		},
	)

	hfilter := gst.NewCapsSimple(
		"video/x-h264",
		glib.Params{
		        "stream-format":    "avc",
		        "alignment":    "au",
		        "profile":    "baseline",
			"width":     int32(320),
			"height":    int32(240),
			"framerate": &gst.Fraction{10, 1},
		},
	)

	h264parse.LinkFiltered(kvssink, hfilter)
	omxh264enc.Link(h264parse)
	conv.LinkFiltered(omxh264enc, filter)
	src.Link(conv)
	//	pl.SetState(gst.STATE_PLAYING)
		pl.SetState(gst.STATE_PAUSED)

	go glib.NewMainLoop(nil).Run()

	return(pl)
}


func main() {

	godotenv.Load(".env")

	pl := setup_pipeline();

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println("opening gpio")
	err := rpio.Open()
	if err != nil {
		panic(fmt.Sprint("unable to open gpio", err.Error()))
	}
	defer rpio.Close()

	host := os.Getenv("AWS_IOT_HOST")
	thingName := os.Getenv("AWS_IOT_DEVICE")
	shadowName := "" // legacy single shadow name

	for _, file := range []string{
		os.Getenv("AWS_IOT_ROOT_CERT"),
		os.Getenv("AWS_IOT_CERT"),
		os.Getenv("AWS_IOT_PRIVATE_KEY"),
	} {
		_, err := os.Stat(file)
		if os.IsNotExist(err) {
			println(file, "not found")
			os.Exit(1)
		}
	}

	cli, err := awsiotdev.New(
		thingName,
		&mqtt.URLDialer{
			URL: fmt.Sprintf("mqtts://%s:8883", host),
			Options: []mqtt.DialOption{
				mqtt.WithTLSCertFiles(
					host,
					os.Getenv("AWS_IOT_ROOT_CERT"),
					os.Getenv("AWS_IOT_CERT"),
					os.Getenv("AWS_IOT_PRIVATE_KEY"),
				),
				mqtt.WithConnStateHandler(func(s mqtt.ConnState, err error) {
					fmt.Printf("%s: %v\n", s, err)
				}),
			},
		},
		mqtt.WithReconnectWait(500*time.Millisecond, 2*time.Second),
	)
	if err != nil {
		panic(err)
	}


	// state flags
	
	loopState := loopStateType{
		reportTime: 1,
		prevCamState: 0,
		prevMotionState: 0,
		motionTrigger: 0,
		safeCounter: 0,
	}

	// Multiplex message handler to route messages to multiple features.
	var mux mqtt.ServeMux
	cli.Handle(&mux)

	s, err := shadow.New(ctx, cli, shadow.WithName(shadowName))
	if err != nil {
		panic(err)
	}
	s.OnError(func(err error) {
		fmt.Printf("async error: %v\n", err)
	})
	s.OnDelta(func(delta shadow.NestedState) {
		go func() {
			// fmt.Printf("\ndelta:\n..............\n%s\n...........\n", prettyDump(delta))
			if (delta["Camera"] != nil) {
				cam := reflect.ValueOf(delta["Camera"]).Float()
				
				if cam > 0.1 {
					// fmt.Printf("\nSHADOW START PLAYING!!\n",)
					pl.SetState(gst.STATE_PLAYING)
					loopState.safeCounter = 0
					_, err := s.Report(ctx, sampleState{Camera: 1})
					if err != nil {
						panic(err)
					}
				
				} else {
					// fmt.Printf("\nSHADOW STOP PLAYING!!\n",)
					pl.SetState(gst.STATE_PAUSED)
					loopState.safeCounter = 0
					_, err := s.Report(ctx, sampleState{Camera: 0})
					if err != nil {
						panic(err)
					}
				}
			}
		}()
	})
	mux.Handle("#", s) // Handle messages for Shadow.

	if _, err := cli.Connect(ctx,
		thingName,
		mqtt.WithKeepAlive(120),
	); err != nil {
		panic(err)
	}

	fmt.Print("\n> to start, update desire and report\n")
	_, err = s.Desire(ctx, sampleState{Camera: 0, Motion: 0 })
	if err != nil {
		panic(err)
	}
	time.Sleep(time.Second)
	doc, err := s.Report(ctx, sampleState{Camera: 0, Motion: 0 })
	if err != nil {
		panic(err)
	}
	fmt.Printf("document:%s", prettyDump(doc))

	time.Sleep(time.Second)
	
	motionPin := rpio.Pin(18)
	motionPin.Input()       // Intput mode

	for (true) {

		motion := motionPin.Read()
		if (motion>loopState.prevMotionState) {  // logic positive edge
			loopState.motionTrigger = 1
			loopState.prevMotionState = motion
			loopState.safeCounter = 0
			loopState.reportTime = 1
		}

		//	fmt.Printf("document:%s", prettyDump(doc))

		// Document stores thing state as map[string]interface{}.
		// You may want to use github.com/mitchellh/mapstructure to
		// converts state to the given struct.

		var typedState sampleState
		loopState.reportTime -= 1

		if ((loopState.reportTime  < 2)||(typedState.Camera>0)) {

			fmt.Print(" \n> get document\n")
			doc, err = s.Get(ctx)
			if err != nil {
				panic(err)
			}
			if err := mapstructure.Decode(doc.State.Desired, &typedState); err != nil {
				panic(err)
			}

			fmt.Printf("\ndocument.State.Desired (typed): %+v\n", typedState)
			loopState.reportTime = 30
			//			fmt.Printf("document:%s", prettyDump(doc))
			fmt.Printf("CAMERA: %+v \n",typedState.Camera);

			if (loopState.motionTrigger>0) {
				fmt.Printf("\nMOTION TRIGGER!!\n",)
				typedState.Camera = 1
				_, err = s.Report(ctx, sampleState{Camera: 1, Motion: 1  })
				if err != nil {
					panic(err)
				}
				loopState.motionTrigger = 0
				time.Sleep(time.Second)
			}

			if (typedState.Camera>loopState.prevCamState) {
				fmt.Printf("\nSTART PLAYING!!\n",)
				pl.SetState(gst.STATE_PLAYING)
				loopState.safeCounter = 0
			} else if (typedState.Camera<loopState.prevCamState) {
				pl.SetState(gst.STATE_PAUSED)
				loopState.safeCounter = 0
				loopState.prevMotionState = 0
			}
			loopState.prevCamState = typedState.Camera

			if (typedState.Camera>0) {
				fmt.Printf("\n\n\nCHECK SAFECOUNTER %d \n\n",loopState.safeCounter)
				loopState.safeCounter += 1
				if (loopState.safeCounter>3) {
					_, err := s.Desire(ctx, sampleState{Camera: 0, Motion: 0 })
					if err != nil {
						panic(err)
					}
					time.Sleep(time.Second)
					_, err = s.Report(ctx, sampleState{Camera: 0, Motion: int(motion) })
					if err != nil {
						panic(err)
					}
					time.Sleep(time.Second)
					loopState.prevCamState = 0
					pl.SetState(gst.STATE_PAUSED)
					loopState.safeCounter = 0
					loopState.prevMotionState = 0
				}
			}

		}
		time.Sleep(1*time.Second)
	}

}

type sampleStruct struct {
	Values []int
}

type sampleState struct {
	Camera  int
	Motion  int
}

type loopStateType struct {
	reportTime int
	prevCamState int
	prevMotionState rpio.State
	motionTrigger int
	safeCounter int
}
