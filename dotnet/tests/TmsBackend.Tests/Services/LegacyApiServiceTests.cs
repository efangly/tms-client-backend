using System.Net;
using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Services;

namespace TmsBackend.Tests.Services;

public class LegacyApiServiceTests
{
    private sealed class RecordingHandler(HttpStatusCode status) : HttpMessageHandler
    {
        public List<(HttpRequestMessage Request, string Body)> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct)
        {
            Requests.Add((request, await request.Content!.ReadAsStringAsync(ct)));
            return new HttpResponseMessage(status);
        }
    }

    private static (LegacyApiService, RecordingHandler) Create(HttpStatusCode status = HttpStatusCode.OK, string token = "secret")
    {
        var handler = new RecordingHandler(status);
        var svc = new LegacyApiService(new HttpClient(handler),
            new AppOptions { LegacyApiUrl = "https://legacy.example/smtrack", LegacyApiToken = token },
            NullLogger<LegacyApiService>.Instance);
        return (svc, handler);
    }

    [Fact]
    public async Task Posts_temp_log_with_bearer_token_and_go_json()
    {
        var (svc, handler) = Create();

        await svc.SendTempLogAsync(new TempLogPayload("ตู้ 1", "00000110", 25.4, 6540, "20261022", "08"));

        var (req, body) = Assert.Single(handler.Requests);
        Assert.Equal(HttpMethod.Post, req.Method);
        Assert.Equal("https://legacy.example/smtrack/legacy/templog", req.RequestUri!.ToString());
        Assert.Equal("Bearer secret", req.Headers.Authorization!.ToString());
        Assert.Equal("""{"mcuId":"ตู้ 1","status":"00000110","tempValue":25.4,"realValue":6540,"date":"20261022","time":"08"}""", body);
    }

    [Fact]
    public async Task Alerts_go_to_the_notification_endpoint()
    {
        var (svc, handler) = Create();

        await svc.SendAlertAsync(new AlertPayload("A", "00000010", 30, 3000, "20261022", "08:00:00", "msg", "high", "A", 1, 2, 8));

        Assert.EndsWith("/legacy/templog/alert/notification", handler.Requests[0].Request.RequestUri!.ToString());
        Assert.Contains("\"alertType\":\"high\"", handler.Requests[0].Body);
    }

    [Fact]
    public async Task Non_success_status_throws()
    {
        var (svc, _) = Create(HttpStatusCode.Unauthorized);
        var e = await Assert.ThrowsAsync<HttpRequestException>(() => svc.SendTempLogAsync(new TempLogPayload("A", "", 0, 0, "", "")));
        Assert.Contains("401", e.Message);
    }

    [Fact]
    public async Task Disabled_without_token_sends_nothing()
    {
        var (svc, handler) = Create(token: "");
        Assert.False(svc.IsEnabled);
        await svc.SendTempLogAsync(new TempLogPayload("A", "", 0, 0, "", ""));
        Assert.Empty(handler.Requests);
    }
}
