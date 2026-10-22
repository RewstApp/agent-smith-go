# Arm the stub broker to lose the device's next PUBACK (sc-126322): the
# message stays in flight and is redelivered with DUP on the next subscribe.
# Env: ENGINE_URL, DEVICE_ID.
$resp = Invoke-RestMethod -Method Post -Uri "$($env:ENGINE_URL)/_control/devices/$($env:DEVICE_ID)/lose-next-puback" -TimeoutSec 30
Write-Output "the broker will lose $($env:DEVICE_ID)'s next PUBACK (armed: $($resp.lose_next_puback))"
