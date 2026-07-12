//go:build windows

package safety

import (
	"os"

	"golang.org/x/sys/windows"
)

func stabiliseWorkspacePath(publicPath, resolvedPath string, expected os.FileInfo) (string, error) {
	if expected != nil {
		current, err := os.Stat(resolvedPath)
		if err != nil || !os.SameFile(current, expected) {
			if err == nil {
				err = &WorkspaceDestinationError{}
			}
			return "", err
		}
	}
	return resolvedPath, nil
}

func openClaimedWorkspaceDirectory(path string) (*os.File, os.FileInfo, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, nil, err
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, nil, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, nil, err
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || owner == nil || user.User.Sid == nil || !owner.Equals(user.User.Sid) {
		_ = windows.CloseHandle(handle)
		if err == nil {
			err = &WorkspaceDestinationError{}
		}
		return nil, nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	info, err := file.Stat()
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_ = file.Close()
		if err == nil {
			err = &WorkspaceDestinationError{}
		}
		return nil, nil, err
	}
	return file, info, nil
}
