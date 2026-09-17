package ntgcalls

/*
#cgo linux LDFLAGS: -L . -lntgcalls -lm -lz
#cgo darwin LDFLAGS: -L . -lntgcalls -lc++ -lz -lbz2 -liconv -framework AVFoundation -framework AudioToolbox -framework CoreAudio -framework QuartzCore -framework CoreMedia -framework VideoToolbox -framework AppKit -framework Metal -framework MetalKit -framework OpenGL -framework IOSurface -framework ScreenCaptureKit
#cgo windows LDFLAGS: -L. -lntgcalls
#include "ntgcalls.h"
#include "../glibc_compatibility.h"
*/
import "C"
