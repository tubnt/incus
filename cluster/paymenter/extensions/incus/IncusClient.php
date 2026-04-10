<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\Log;

/**
 * Incus REST API mTLS 客户端
 *
 * 通过 mTLS 证书连接 Incus 集群 API，自动附加 project 参数，
 * 支持异步操作等待、超时重试。
 */
class IncusClient
{
    private string $endpoint;
    private string $certFile;
    private string $keyFile;
    private string $project;
    private int $timeout;
    private int $maxRetries;

    public function __construct(array $config)
    {
        $this->endpoint = rtrim($config['endpoint'], '/');
        $this->certFile = $config['cert_file'];
        $this->keyFile = $config['key_file'];
        $this->project = $config['project'] ?? 'customers';
        $this->timeout = $config['timeout'] ?? 30;
        $this->maxRetries = $config['max_retries'] ?? 3;
    }

    /**
     * GET 请求
     */
    public function get(string $path, array $query = []): array
    {
        return $this->request('GET', $path, [], $query);
    }

    /**
     * POST 请求
     */
    public function post(string $path, array $data = []): array
    {
        return $this->request('POST', $path, $data);
    }

    /**
     * PUT 请求
     */
    public function put(string $path, array $data = []): array
    {
        return $this->request('PUT', $path, $data);
    }

    /**
     * PATCH 请求
     */
    public function patch(string $path, array $data = []): array
    {
        return $this->request('PATCH', $path, $data);
    }

    /**
     * DELETE 请求
     */
    public function delete(string $path): array
    {
        return $this->request('DELETE', $path);
    }

    /**
     * 发送请求并自动处理异步操作
     *
     * Incus 对写操作返回 operation 对象，需要 poll 等待完成。
     */
    public function request(string $method, string $path, array $data = [], array $query = []): array
    {
        $query['project'] = $this->project;
        $url = $this->endpoint . $path . '?' . http_build_query($query);

        $attempt = 0;
        $lastException = null;

        while ($attempt < $this->maxRetries) {
            $attempt++;
            try {
                $response = $this->doRequest($method, $url, $data);

                // 异步操作：type=async 时需要等待完成
                if (isset($response['type']) && $response['type'] === 'async') {
                    return $this->waitForOperation($response['operation']);
                }

                return $response;
            } catch (\RuntimeException $e) {
                $lastException = $e;
                Log::warning('Incus API 请求失败', [
                    'method' => $method,
                    'path' => $path,
                    'attempt' => $attempt,
                    'error' => $e->getMessage(),
                ]);

                if ($attempt < $this->maxRetries) {
                    // 指数退避：1s, 2s, 4s...
                    usleep(pow(2, $attempt - 1) * 1000000);
                }
            }
        }

        throw new \RuntimeException(
            "Incus API 请求失败，已重试 {$this->maxRetries} 次: " . $lastException->getMessage(),
            0,
            $lastException
        );
    }

    /**
     * 等待异步操作完成
     */
    private function waitForOperation(string $operationUrl): array
    {
        // 使用 Incus 的 wait 端点，带超时
        $waitPath = $operationUrl . '/wait';
        $url = $this->endpoint . $waitPath . '?' . http_build_query([
            'project' => $this->project,
            'timeout' => $this->timeout,
        ]);

        $response = $this->doRequest('GET', $url);

        if (isset($response['metadata']['status']) && $response['metadata']['status'] === 'Failure') {
            $errMsg = $response['metadata']['err'] ?? '未知错误';
            throw new \RuntimeException("Incus 异步操作失败: {$errMsg}");
        }

        return $response;
    }

    /**
     * 执行 cURL 请求（mTLS）
     */
    private function doRequest(string $method, string $url, array $data = []): array
    {
        $ch = curl_init();

        curl_setopt_array($ch, [
            CURLOPT_URL => $url,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => $this->timeout,
            CURLOPT_CONNECTTIMEOUT => 10,

            // mTLS 证书
            CURLOPT_SSLCERT => $this->certFile,
            CURLOPT_SSLKEY => $this->keyFile,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,

            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => [
                'Content-Type: application/json',
                'Accept: application/json',
            ],
        ]);

        if (!empty($data) && in_array($method, ['POST', 'PUT', 'PATCH'])) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($data));
        }

        $responseBody = curl_exec($ch);
        $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $curlError = curl_error($ch);
        $curlErrno = curl_errno($ch);
        curl_close($ch);

        if ($curlErrno !== 0) {
            throw new \RuntimeException("cURL 错误 [{$curlErrno}]: {$curlError}");
        }

        $decoded = json_decode($responseBody, true);
        if ($decoded === null) {
            throw new \RuntimeException("Incus API 返回无效 JSON，HTTP {$httpCode}: {$responseBody}");
        }

        if ($httpCode >= 400) {
            $errMsg = $decoded['error'] ?? "HTTP {$httpCode}";
            Log::error('Incus API 错误', [
                'method' => $method,
                'url' => $url,
                'http_code' => $httpCode,
                'error' => $errMsg,
            ]);
            throw new \RuntimeException("Incus API 错误: {$errMsg}");
        }

        return $decoded;
    }
}
