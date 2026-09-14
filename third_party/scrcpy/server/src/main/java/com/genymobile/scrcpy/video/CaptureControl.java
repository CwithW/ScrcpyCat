package com.genymobile.scrcpy.video;

import android.media.MediaCodec;
import android.os.Bundle;

import com.genymobile.scrcpy.util.Ln;

public class CaptureControl {

    public static final int RESET_REASON_TERMINATED = 1;
    public static final int RESET_REASON_DISPLAY_PROPERTIES_CHANGED = 1 << 1;
    public static final int RESET_REASON_CLIENT_RESET = 1 << 2;
    public static final int RESET_REASON_CLIENT_RESIZED = 1 << 3;

    private int reset = 0;

    // Current instance of MediaCodec to "interrupt" on reset
    private MediaCodec runningMediaCodec;
    private int videoBitrate;

    public synchronized boolean isResetRequested() {
        return reset != 0;
    }

    public synchronized int consumeReset() {
        int value = reset;
        reset = 0;
        return value;
    }

    public synchronized void reset(int reason) {
        assert reason != 0;
        reset |= reason;
        if (runningMediaCodec != null) {
            try {
                runningMediaCodec.signalEndOfInputStream();
            } catch (IllegalStateException e) {
                // ignore
            }
        }
    }

    public synchronized void setRunningMediaCodec(MediaCodec runningMediaCodec) {
        this.runningMediaCodec = runningMediaCodec;
        if (runningMediaCodec != null && videoBitrate > 0) {
            applyVideoBitrate();
        }
    }

    public synchronized void setVideoBitrate(int bitrate) {
        if (bitrate < 100000 || bitrate > 100000000) {
            return;
        }
        videoBitrate = bitrate;
        applyVideoBitrate();
    }

    private void applyVideoBitrate() {
        if (runningMediaCodec != null) {
            Bundle parameters = new Bundle();
            parameters.putInt(MediaCodec.PARAMETER_KEY_VIDEO_BITRATE, videoBitrate);
            try {
                runningMediaCodec.setParameters(parameters);
                Ln.d("Video bitrate: " + videoBitrate);
            } catch (IllegalStateException | IllegalArgumentException e) {
                Ln.w("Encoder rejected bitrate update: " + e.getMessage());
            }
        }
    }

    public synchronized void requestKeyframe() {
        if (runningMediaCodec != null) {
            Bundle parameters = new Bundle();
            parameters.putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0);
            try {
                runningMediaCodec.setParameters(parameters);
            } catch (IllegalStateException | IllegalArgumentException e) {
                reset(RESET_REASON_CLIENT_RESET);
            }
        }
    }
}
