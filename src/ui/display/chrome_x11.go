//go:build linux && ((!x11 && !wayland) || x11)

package display

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
#include <X11/Xlib.h>

// libX11 is loaded at run time (GLFW's X11 backend does the same), so the binary has no
// link-time dependency on it and still starts on systems without X11.
typedef Atom (*fs_intern_fn)(Display*, const char*, Bool);
typedef int (*fs_ungrab_fn)(Display*, Time);
typedef Status (*fs_send_fn)(Display*, Window, Bool, long, XEvent*);
typedef int (*fs_flush_fn)(Display*);
typedef Bool (*fs_query_fn)(Display*, Window, Window*, Window*, int*, int*, int*, int*, unsigned int*);

static void *fs_lib(void) {
	static void *h;
	if (!h) h = dlopen("libX11.so.6", RTLD_LAZY);
	return h;
}

// fs_query reads the pointer: root position and button mask. 0 on success.
static int fs_query(void *d, int *rx, int *ry, unsigned int *mask) {
	void *h = fs_lib();
	if (!h || !d) return -1;
	fs_query_fn q = (fs_query_fn)dlsym(h, "XQueryPointer");
	if (!q) return -1;
	Window root = RootWindow((Display*)d, DefaultScreen((Display*)d));
	Window r, c; int wx, wy;
	if (!q((Display*)d, root, &r, &c, rx, ry, &wx, &wy, mask)) return -1;
	return 0;
}

// fs_moveresize sends _NET_WM_MOVERESIZE for the window. dir: 0..7 resize (EWMH order),
// 8 move. Returns 0 when the message was sent.
static int fs_moveresize(void *d, unsigned long win, int dir) {
	void *h = fs_lib();
	if (!h || !d) return -1;
	fs_intern_fn intern = (fs_intern_fn)dlsym(h, "XInternAtom");
	fs_ungrab_fn ungrab = (fs_ungrab_fn)dlsym(h, "XUngrabPointer");
	fs_send_fn send = (fs_send_fn)dlsym(h, "XSendEvent");
	fs_flush_fn flush = (fs_flush_fn)dlsym(h, "XFlush");
	if (!intern || !ungrab || !send || !flush) return -1;
	int rx, ry; unsigned int mask;
	if (fs_query(d, &rx, &ry, &mask) != 0) return -1;
	if (!(mask & Button1Mask)) return -2;
	Display *dpy = (Display*)d;
	XEvent ev;
	for (unsigned i = 0; i < sizeof ev; i++) ((char*)&ev)[i] = 0;
	ev.xclient.type = ClientMessage;
	ev.xclient.window = win;
	ev.xclient.message_type = intern(dpy, "_NET_WM_MOVERESIZE", False);
	ev.xclient.format = 32;
	ev.xclient.data.l[0] = rx;
	ev.xclient.data.l[1] = ry;
	ev.xclient.data.l[2] = dir;
	ev.xclient.data.l[3] = 1; // Button1
	ev.xclient.data.l[4] = 1; // source: normal application
	ungrab(dpy, CurrentTime);
	send(dpy, RootWindow(dpy, DefaultScreen(dpy)), False,
		SubstructureNotifyMask | SubstructureRedirectMask, &ev);
	flush(dpy);
	return 0;
}

static int fs_button_down(void *d) {
	int rx, ry; unsigned int mask;
	if (fs_query(d, &rx, &ry, &mask) != 0) return 0;
	return (mask & Button1Mask) != 0;
}
*/
import "C"

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
)

func init() {
	nativeMoveResize = x11MoveResize
	nativeButtonDown = x11ButtonDown
}

// _NET_WM_MOVERESIZE directions (EWMH).
func netWMDirection(e Edge) C.int {
	switch e {
	case EdgeTopLeft:
		return 0
	case EdgeTop:
		return 1
	case EdgeTopRight:
		return 2
	case EdgeRight:
		return 3
	case EdgeBottomRight:
		return 4
	case EdgeBottom:
		return 5
	case EdgeBottomLeft:
		return 6
	case EdgeLeft:
		return 7
	}
	return 8 // move
}

// x11MoveResize returns false when the platform is not X11 or the request failed.
func x11MoveResize(vp *glfw.Window, e Edge) (ok bool) {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return false
	}
	dpy := unsafe.Pointer(glfw.GetX11Display())
	return C.fs_moveresize(dpy, C.ulong(vp.GetX11Window()), netWMDirection(e)) == 0
}

func x11ButtonDown(vp *glfw.Window) bool {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return false
	}
	return C.fs_button_down(unsafe.Pointer(glfw.GetX11Display())) != 0
}
