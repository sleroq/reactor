package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tgerr"
	qrcode "github.com/yeqown/go-qrcode/v2"
	"go.uber.org/zap"
	"golang.org/x/term"
)

type terminalQRWriter struct {
	output *os.File
}

func (w terminalQRWriter) Write(matrix qrcode.Matrix) error {
	bitmap := matrix.Bitmap()
	const quietZone = 2

	var output strings.Builder
	lineWidth := (len(bitmap[0]) + quietZone*2) * 2
	output.Grow((len(bitmap)+quietZone*2)*(lineWidth+1) + 1)
	output.WriteByte('\n')

	for range quietZone {
		output.WriteString(strings.Repeat("  ", len(bitmap[0])+quietZone*2))
		output.WriteByte('\n')
	}

	for _, row := range bitmap {
		output.WriteString(strings.Repeat("  ", quietZone))
		for _, cell := range row {
			if cell {
				output.WriteString("██")
				continue
			}
			output.WriteString("  ")
		}
		output.WriteString(strings.Repeat("  ", quietZone))
		output.WriteByte('\n')
	}

	for range quietZone {
		output.WriteString(strings.Repeat("  ", len(bitmap[0])+quietZone*2))
		output.WriteByte('\n')
	}

	_, err := w.output.WriteString(output.String())
	return err
}

func (terminalQRWriter) Close() error {
	return nil
}

func renderQRCode(token qrlogin.Token, output *os.File) error {
	qrCode, err := qrcode.New(token.URL())
	if err != nil {
		return fmt.Errorf("create QR code: %w", err)
	}

	if err := qrCode.Save(terminalQRWriter{output: output}); err != nil {
		return fmt.Errorf("render QR code: %w", err)
	}

	return nil
}

func promptPassword() (string, error) {
	fmt.Print("Enter 2FA password: ")
	password, err := term.ReadPassword(syscall.Stdin)
	fmt.Println()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(password)), nil
}

func authViaQR(ctx context.Context, client *telegram.Client, loggedIn qrlogin.LoggedIn, logger *zap.SugaredLogger) error {
	status, err := client.Auth().Status(ctx)
	if err != nil {
		return fmt.Errorf("check authorization: %w", err)
	}
	if status.Authorized {
		logger.Info("Session is already authorized")
		return nil
	}

	logger.Info("Signing in with QR code")
	_, err = client.QR().Auth(ctx, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
		fmt.Print("\033[H\033[2J")
		fmt.Println("Scan the QR code in Telegram: Settings -> Devices -> Link Desktop Device")
		fmt.Println("Waiting for scan...")

		if err := renderQRCode(token, os.Stdout); err != nil {
			return err
		}

		logger.Infow("QR login token issued",
			"expires_at", token.Expires(),
			"expires_in", max(time.Until(token.Expires()).Round(time.Second), 0),
		)
		return nil
	})
	if err != nil {
		if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
			password, promptErr := promptPassword()
			if promptErr != nil {
				return fmt.Errorf("read 2FA password: %w", promptErr)
			}

			if _, passwordErr := client.Auth().Password(ctx, password); passwordErr != nil {
				return fmt.Errorf("sign in with 2FA password: %w", passwordErr)
			}

			logger.Info("Signed in with QR code and 2FA")
			return nil
		}

		return fmt.Errorf("sign in with QR code: %w", err)
	}

	logger.Info("Signed in with QR code")
	return nil
}
