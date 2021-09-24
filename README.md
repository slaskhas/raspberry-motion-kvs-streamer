# raspberry-motion-kvs-streamer

A demo program showing how to produce Kinesis Video Stream in Golang using a gstreamer pipeline, on a raspberry. It can be controlled through a flag in the *AWS IoT shadow* state or by a pin, controlled by a PIR motion sensor.

### Pre requirements

To build and test you need a raspberry with a working camera. You also need **gstreamer** and  **kvssink** gstreamer plugin from [https://github.com/awslabs/amazon-kinesis-video-streams-producer-sdk-cpp](https://github.com/awslabs/amazon-kinesis-video-streams-producer-sdk-cpp).
Unless you are cross compiling you need Go for raspberry, which may be downloaded from [https://golang.org/dl/](https://golang.org/dl/) .

You also need an AWS account where you have created a **Kinesis Video Stream** . I suggest you create a separate user on your AWS account for the streamer, with very limited permissions , and no consol access.
Since you will be using AWS IoT Core , you need to create a device and it's keys.

### Environment variables

Settings are sent to the app through environment variables.
The following varables are required:

* **KVSSINK<ins> </ins>LOG<ins> </ins>CONFIG**  Used to direct debug logs from the kvssink plugin , may be **/dev/null**
* **AWS<ins> </ins>ACCESS<ins> </ins>KEY<ins> </ins>ID**  AWS key for the IAM user created for the streamer
* **AWS<ins> </ins>SECRET<ins> </ins>ACCESS_KEY**  AWS secret for the IAM user created for the streamer
* **AWS<ins> </ins>REGION**  Where your stream is create , i.e. us-west-2
* **STREAM<ins> </ins>NAME** Name of the KVS Stream
* **AWS<ins> </ins>IOT<ins> </ins>HOST**  Endpoint for your AWS IoT Core
* **AWS<ins> </ins>IOT<ins> </ins>DEVICE**  Device name in AWS IoT Core 
* **AWS<ins> </ins>IOT<ins> </ins>ROOT<ins> </ins>CERT**  Path to your root cert file (/foo/bar/AmazonRootCA1.crt)
* **AWS<ins> </ins>IOT<ins> </ins>CERT** Path to your device cert file
* **AWS<ins> </ins>IOT<ins> </ins>PRIVATE<ins> </ins>KEY**    Path to your device key file

The variables may be set using a .env file, or set/exported from your shell.

### Verify your environment

To verify that your streaming environment, try the following gstreamer pipeline: 

<pre>
gst-launch-1.0  v4l2src do-timestamp=TRUE device=/dev/video0 !
 videoconvert ! 
 video/x-raw,format=I420,width=320,height=240,framerate=10/1 !
 omxh264enc control-rate=1 target-bitrate=2560000 periodicity-idr=45 
 inline-header=FALSE ! h264parse ! 
 video/x-h264,stream-format=avc,alignment=au,width=320,height=240,
 framerate=10/1,profile=baseline ! 
 kvssink stream-name=$STREAM_NAME access-key=$AWS_ACCESS_KEY_ID 
 secret-key=$AWS_SECRET_ACCESS_KEY aws-region=$AWS_REGION 
</pre>

You should be able to view your video live on the AWS Console under Kinesis Video Streams -> Video streams -> your stream-name -> Media Playback.

### Build the app

go build motion-kvs-streamer.go shortprettydump.go <br/>
It may take a while on a pi zero.

### Try it

As root , set the environment variables or make sure the .env file is correct.
Then run the binary i.e. **./motion-kvs-streamer**

*Optionally* connect a standard PIR motion sensor to +5V  , Gnd and pin 18 on your pi and start moving around. It's safe to use the +5V because the PIR sensor has a built in 3.3v regulator. If in doubt , verify your brand/model.

If you don't have a PIR motion detector connected you can start the camera by setting the shadow flag *Camera* to 1 , in the AWS Console -> AWS IoT -> Manage -> Things -> your-thing-name -> Device Shadows -> Classic Shadow -> Edit button and paste

<pre>
{
  "state": {
    "desired": {
      "Camera": 1,
      "Motion": 0
    },
    "reported": {
      "Camera": 0,
      "Motion": 0
    }
  }
}
</pre>

Click update.

### Make it start on boot

Edit the file **kvsstreamer.service** to match the location of the binary **motion-kvs-streamer** created above , then as root:

<pre>
cp kvsstreamer.service /etc/systemd/system/
systemctl enable kvsstreamer
systemctl start kvsstreamer
systemctl status kvsstreamer
</pre>


### Some comments about the code

The code is heavily based on [https://github.com/seqsense/aws-iot-device-sdk-go](https://github.com/seqsense/aws-iot-device-sdk-go) and the shadow example. The file **shortprettydump.go**  is a straight copy from that example. [github.com/ziutek/gst](github.com/ziutek/gst) is used for gstreamer stuff. There are other maintained libraries for gstreamer, but I found this one to compile without problems on my pi zero. 

The code is a bit messy with a bunch of flags, mainly because I wanted to have the stream start quickly on motion, and then run for a limited time. Plus I wanted to have it stay as quiet as possible over the network when there is no activity. My real-world use case is using a 4G Cell connection causing those requirements.


