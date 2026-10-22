using System.Net.Http.Headers;
using System.Net.Http.Json;
using TmsBackend.Config;
using TmsBackend.Json;

namespace TmsBackend.Services;

public interface ILegacyApi
{
    /// <summary>True when both LEGACY_API_URL and LEGACY_API_TOKEN are set.</summary>
    bool IsEnabled { get; }
    Task SendTempLogAsync(TempLogPayload payload, CancellationToken ct = default);
    Task SendTempLogBatchAsync(IReadOnlyList<TempLogPayload> payloads, CancellationToken ct = default);
    Task SendAlertAsync(AlertPayload payload, CancellationToken ct = default);
}

/// <summary>POSTs readings and alerts to the Legacy API with a bearer token (api_notification.go).</summary>
public sealed class LegacyApiService(HttpClient http, AppOptions options, ILogger<LegacyApiService> logger) : ILegacyApi
{
    public bool IsEnabled => options.LegacyApiUrl != "" && options.LegacyApiToken != "";

    public Task SendTempLogAsync(TempLogPayload payload, CancellationToken ct = default) =>
        SendAsync("/legacy/templog", payload, "temp log", ct);

    public Task SendTempLogBatchAsync(IReadOnlyList<TempLogPayload> payloads, CancellationToken ct = default) =>
        SendAsync("/legacy/templog/batch", payloads, "temp log batch", ct);

    public Task SendAlertAsync(AlertPayload payload, CancellationToken ct = default) =>
        SendAsync("/legacy/templog/alert/notification", payload, "alert notification", ct);

    private async Task SendAsync<T>(string path, T payload, string description, CancellationToken ct)
    {
        if (!IsEnabled)
            return;

        var url = options.LegacyApiUrl + path;
        using var request = new HttpRequestMessage(HttpMethod.Post, url)
        {
            Content = JsonContent.Create(payload, options: GoJson.Options),
        };
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", options.LegacyApiToken);

        HttpResponseMessage response;
        try
        {
            response = await http.SendAsync(request, ct);
        }
        catch (Exception e) when (e is HttpRequestException or TaskCanceledException && !ct.IsCancellationRequested)
        {
            logger.LogError("API Notification - Failed to send {Description} to {Url}: {Error}", description, url, e.Message);
            throw new HttpRequestException($"failed to send {description}: {e.Message}", e);
        }

        using (response)
        {
            if (!response.IsSuccessStatusCode)
            {
                logger.LogError("API Notification - {Description} request failed with status {Status} (url={Url})", description, (int)response.StatusCode, url);
                throw new HttpRequestException($"{description} request failed with status: {(int)response.StatusCode}");
            }
        }
        logger.LogInformation("Successfully sent {Description} to API", description);
    }
}
