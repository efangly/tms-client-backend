using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Infrastructure;
using TmsBackend.Services;

namespace TmsBackend.Tests.Services;

public class MqttServiceTests
{
    // Found by the e2e run: an empty MQTT_BROKER (MQTT disabled) must not crash startup.
    [Fact]
    public async Task Empty_broker_is_disabled_and_does_not_throw()
    {
        var svc = new MqttService(new AppOptions { MqttBroker = "" }, new AppReadiness(), NullLogger<MqttService>.Instance);

        Assert.False(svc.IsEnabled);
        Assert.False(svc.IsConnected);
        await svc.StartAsync(default);
        await svc.StopAsync(default);
    }
}
