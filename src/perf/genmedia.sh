#!/bin/sh
# Generates the preview test media under ${1:-/tmp/fsprof} (a few seconds each, nice'd).
d=${1:-/tmp/fsprof}; mkdir -p "$d"; cd "$d" || exit 1
n="nice -n 19 ffmpeg -hide_banner -loglevel error -y -threads 4"
$n -f lavfi -i testsrc2=size=1920x1080:rate=30 -t 8 -c:v libx264 -preset veryfast -g 250 -pix_fmt yuv420p h264_1080p30.mp4
$n -f lavfi -i testsrc2=size=3840x2160:rate=30 -t 6 -c:v libx264 -preset veryfast -g 250 -pix_fmt yuv420p h264_4k30.mp4
$n -f lavfi -i testsrc2=size=1920x1080:rate=30 -t 8 -c:v libx264 -preset veryfast -g 1 -pix_fmt yuv420p intra_1080p30.mp4
$n -f lavfi -i testsrc2=size=1920x1080:rate=60000/1001 -t 8 -c:v libx264 -preset veryfast -g 250 -pix_fmt yuv420p h264_1080p5994.mp4
$n -f lavfi -i testsrc2=size=3840x2160:rate=30 -t 6 -c:v libx265 -preset ultrafast -x265-params log-level=error:pools=4 -pix_fmt yuv420p hevc_4k30.mp4
# camera-like bitrates (noise makes decode realistically expensive)
$n -f lavfi -i "testsrc2=size=3840x2160:rate=30,noise=alls=20:allf=t+u" -t 6 -c:v libx264 -preset veryfast -g 250 -b:v 80M -maxrate 80M -bufsize 160M -pix_fmt yuv420p h264_4k30_hb.mp4
$n -f lavfi -i "testsrc2=size=1920x1080:rate=30,noise=alls=20:allf=t+u" -t 8 -c:v libx264 -preset veryfast -g 250 -b:v 30M -maxrate 30M -bufsize 60M -pix_fmt yuv420p h264_1080p30_hb.mp4
