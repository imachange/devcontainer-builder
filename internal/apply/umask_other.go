//go:build !unix

package apply

// setUmask は、unix 以外では、何もしない。
func setUmask() {}
