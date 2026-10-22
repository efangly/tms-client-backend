using System.Text.Json;
using MQTTnet;
using MQTTnet.Protocol;
using TmsBackend.Config;
using TmsBackend.Infrastructure;
using TmsBackend.Json;

namespace TmsBackend.Services;

public interface IMqttPublisher
{
    /// <summary>True when MQTT_BROKER is configured.</summary>
    bool IsEnabled { get; }
    bool IsConnected { get; }
    Task PublishTemperatureBatchAsync(IReadOnlyList<MqttTemperaturePayload> payloads, CancellationToken ct = default);
}

/// <summary>
/// Publishes readings to the MQTT broker (mqtt_service.go). Connects once
/// startup is ready, then keeps reconnecting with backoff (up to 30 s) after
/// any disconnect — including when the very first connect fails, which Go
/// never recovered from.
/// </summary>
public sealed class MqttService : BackgroundService, IMqttPublisher
{
    private static readonly TimeSpan MaxReconnectDelay = TimeSpan.FromSeconds(30);

    private readonly AppOptions _options;
    private readonly AppReadiness _readiness;
    private readonly ILogger<MqttService> _logger;
    private readonly IMqttClient _client;
    private readonly MqttClientOptions _clientOptions;
    private readonly SemaphoreSlim _connectLock = new(1, 1);
    private readonly SemaphoreSlim _disconnected = new(0, 1);

    public MqttService(AppOptions options, AppReadiness readiness, ILogger<MqttService> logger)
    {
        _options = options;
        _readiness = readiness;
        _logger = logger;
        ClientId = options.MqttClientId != "" ? options.MqttClientId : $"tms-backend-{DateTime.UtcNow.Ticks * 100}";

        var builder = new MqttClientOptionsBuilder()
            .WithTcpServer(options.MqttBroker, int.TryParse(options.MqttPort, out var port) ? port : 1883)
            .WithClientId(ClientId)
            .WithKeepAlivePeriod(TimeSpan.FromSeconds(60))
            .WithCleanSession(true);
        if (options.MqttUsername != "" || options.MqttPassword != "")
            builder = builder.WithCredentials(options.MqttUsername, options.MqttPassword);
        _clientOptions = builder.Build();

        _client = new MqttClientFactory().CreateMqttClient();
        _client.DisconnectedAsync += e =>
        {
            if (e.ClientWasConnected)
            {
                _logger.LogError("MQTT connection lost: {Reason}", e.Exception?.Message ?? e.Reason.ToString());
                // Wake the reconnect loop; one pending signal is enough.
                if (_disconnected.CurrentCount == 0)
                {
                    try { _disconnected.Release(); }
                    catch (SemaphoreFullException) { /* raced with another signal */ }
                }
            }
            return Task.CompletedTask;
        };
    }

    public string ClientId { get; }

    public bool IsEnabled => _options.MqttBroker != "";

    public bool IsConnected => IsEnabled && _client.IsConnected;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        if (!IsEnabled)
        {
            _logger.LogInformation("MQTT: DISABLED (MQTT_BROKER not configured)");
            return;
        }

        await _readiness.WaitAsync(stoppingToken);
        _logger.LogInformation("Initializing MQTT service...");

        // Startup: 5 attempts with backoff (main.go), then keep going in the loop below.
        try
        {
            await Startup.RetryWithBackoffAsync("MQTT connection", ConnectAsync, 5,
                TimeSpan.FromSeconds(2), MaxReconnectDelay, _logger, stoppingToken);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            _logger.LogError("Failed to connect to MQTT broker after retries: {Error} (will keep retrying)", e.Message);
        }

        var delay = TimeSpan.FromSeconds(1);
        while (!stoppingToken.IsCancellationRequested)
        {
            if (_client.IsConnected)
            {
                delay = TimeSpan.FromSeconds(1);
                await _disconnected.WaitAsync(stoppingToken);
                continue;
            }
            await Task.Delay(delay, stoppingToken);
            try
            {
                await ConnectAsync(stoppingToken);
                _logger.LogInformation("MQTT reconnected to broker");
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                _logger.LogWarning("MQTT reconnect failed: {Error}", e.Message);
                delay = TimeSpan.FromTicks(Math.Min(delay.Ticks * 2, MaxReconnectDelay.Ticks));
            }
        }
    }

    private async Task ConnectAsync(CancellationToken ct)
    {
        await _connectLock.WaitAsync(ct);
        try
        {
            if (_client.IsConnected)
                return;
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
            timeout.CancelAfter(TimeSpan.FromSeconds(30));
            await _client.ConnectAsync(_clientOptions, timeout.Token);
            _logger.LogInformation("MQTT connected to {Broker}:{Port} (clientID: {ClientId}), topic: {Topic}",
                _options.MqttBroker, _options.MqttPort, ClientId, _options.MqttTopic);
        }
        finally
        {
            _connectLock.Release();
        }
    }

    /// <summary>Publishes all readings as one JSON array message, QoS 0, not retained.</summary>
    public async Task PublishTemperatureBatchAsync(IReadOnlyList<MqttTemperaturePayload> payloads, CancellationToken ct = default)
    {
        if (!_client.IsConnected)
            await ConnectAsync(ct);

        var message = new MqttApplicationMessageBuilder()
            .WithTopic(_options.MqttTopic)
            .WithPayload(JsonSerializer.SerializeToUtf8Bytes(payloads, GoJson.Options))
            .WithQualityOfServiceLevel(MqttQualityOfServiceLevel.AtMostOnce)
            .WithRetainFlag(false)
            .Build();
        var result = await _client.PublishAsync(message, ct);
        if (!result.IsSuccess)
            throw new InvalidOperationException($"MQTT batch publish failed: {result.ReasonCode}");
        _logger.LogInformation("Published {Count} readings to MQTT topic: {Topic}", payloads.Count, _options.MqttTopic);
    }

    public override async Task StopAsync(CancellationToken cancellationToken)
    {
        await base.StopAsync(cancellationToken);
        if (_client.IsConnected)
        {
            try
            {
                await _client.DisconnectAsync(new MqttClientDisconnectOptionsBuilder().Build(), cancellationToken);
                _logger.LogInformation("MQTT disconnected");
            }
            catch (Exception e)
            {
                _logger.LogWarning("MQTT disconnect failed: {Error}", e.Message);
            }
        }
    }

    public override void Dispose()
    {
        _client.Dispose();
        _connectLock.Dispose();
        _disconnected.Dispose();
        base.Dispose();
    }
}
