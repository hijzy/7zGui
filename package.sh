#!/bin/bash
set -e

APP_NAME="7zGui"
FYNE_CMD="$(go env GOPATH)/bin/fyne"

echo "Cleaning up..."
rm -rf "$APP_NAME.app"

echo "Packaging..."
$FYNE_CMD package -os darwin -name "$APP_NAME"

echo "Copying resources..."
mkdir -p "$APP_NAME.app/Contents/Resources"
cp 7zz "$APP_NAME.app/Contents/Resources/"
cp NotoSansSC-Regular.ttf "$APP_NAME.app/Contents/Resources/"
cp Icon.png "$APP_NAME.app/Contents/Resources/"

echo "Done! App is at $APP_NAME.app"
