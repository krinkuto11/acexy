#!/usr/bin/env python3
"""
Integration test for acexy with acestream orchestrator.
Tests that acexy correctly provisions acestream-specific containers via orchestrator.
"""

import os
import sys
import time
import subprocess
import requests
import signal
import json
import docker
from datetime import datetime
from typing import List, Dict

# Add app to path for imports
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'acestream-orchestrator-main/app'))

def setup_test_environment():
    """Set up the test environment variables."""
    test_env = os.environ.copy()
    test_env.update({
        'MIN_REPLICAS': '0',  # Start with no replicas for testing
        'MAX_REPLICAS': '5',
        'APP_PORT': '8004',  # Use different port to avoid conflicts
        'TARGET_IMAGE': 'hello-world',  # Use lightweight image for testing
        'CONTAINER_LABEL': 'acexy.integration.test=true',
        'STARTUP_TIMEOUT_S': '10',
        'API_KEY': 'test-acexy-integration-key',
        'PORT_RANGE_HOST': '29100-29199',
        'ACE_HTTP_RANGE': '50100-50199', 
        'ACE_HTTPS_RANGE': '51100-51199',
        'AUTO_DELETE': 'false',  # Don't auto-delete for testing
        'ACEXY_LOG_LEVEL': 'DEBUG',
    })
    return test_env

def start_orchestrator(test_env: Dict[str, str]) -> subprocess.Popen:
    """Start the orchestrator process."""
    print("🚀 Starting orchestrator...")
    proc = subprocess.Popen([
        sys.executable, '-m', 'uvicorn',
        'app.main:app',
        '--host', '0.0.0.0',
        '--port', test_env['APP_PORT']
    ], 
    env=test_env, 
    stdout=subprocess.PIPE, 
    stderr=subprocess.PIPE,
    cwd='acestream-orchestrator-main')
    
    # Wait for startup
    time.sleep(3)
    return proc

def wait_for_orchestrator(port: str) -> bool:
    """Wait for orchestrator to be ready."""
    for i in range(10):
        try:
            response = requests.get(f'http://localhost:{port}/engines', timeout=2)
            if response.status_code == 200:
                print("✅ Orchestrator is ready")
                return True
        except Exception:
            pass
        time.sleep(1)
    return False

def test_acestream_provision_endpoint(port: str, api_key: str) -> bool:
    """Test the acestream-specific provision endpoint."""
    print("\n📋 Testing acestream provision endpoint...")
    
    try:
        headers = {
            'Authorization': f'Bearer {api_key}',
            'Content-Type': 'application/json'
        }
        
        provision_data = {
            'labels': {'test.acestream': 'provision'},
            'env': {'TEST_VAR': 'acestream_test'},
            'image': 'hello-world'  # Use lightweight image for testing
        }
        
        response = requests.post(
            f'http://localhost:{port}/provision/acestream',
            headers=headers,
            json=provision_data,
            timeout=30
        )
        
        if response.status_code == 200:
            result = response.json()
            print(f"✅ Acestream provision successful")
            print(f"   Container ID: {result.get('container_id', 'N/A')}")
            print(f"   Host HTTP Port: {result.get('host_http_port', 'N/A')}")
            print(f"   Container HTTP Port: {result.get('container_http_port', 'N/A')}")
            print(f"   Container HTTPS Port: {result.get('container_https_port', 'N/A')}")
            
            # Verify response has required fields
            required_fields = ['container_id', 'host_http_port', 'container_http_port', 'container_https_port']
            for field in required_fields:
                if field not in result:
                    print(f"❌ Missing required field: {field}")
                    return False
            
            return True
        else:
            print(f"❌ Acestream provision failed: {response.status_code}")
            print(f"   Response: {response.text}")
            return False
            
    except Exception as e:
        print(f"❌ Error testing acestream provision: {e}")
        return False

def test_multiple_unique_ports(port: str, api_key: str, num_containers: int = 3) -> bool:
    """Test that multiple acestream containers get unique ports."""
    print(f"\n📋 Testing multiple acestream containers for unique ports...")
    
    containers = []
    used_ports = set()
    
    try:
        headers = {
            'Authorization': f'Bearer {api_key}',
            'Content-Type': 'application/json'
        }
        
        for i in range(num_containers):
            provision_data = {
                'labels': {'test.multiple': f'container_{i}'},
                'env': {},
                'image': 'hello-world'
            }
            
            response = requests.post(
                f'http://localhost:{port}/provision/acestream',
                headers=headers,
                json=provision_data,
                timeout=30
            )
            
            if response.status_code == 200:
                result = response.json()
                containers.append(result)
                
                host_port = result['host_http_port']
                container_http = result['container_http_port']
                container_https = result['container_https_port']
                
                print(f"   Container {i+1}: Host port {host_port}, HTTP {container_http}, HTTPS {container_https}")
                
                # Check for port conflicts
                if host_port in used_ports:
                    print(f"❌ Port conflict detected! Host port {host_port} was reused")
                    return False
                
                used_ports.add(host_port)
                
            else:
                print(f"❌ Failed to provision container {i+1}: {response.status_code}")
                return False
        
        print(f"✅ Successfully provisioned {len(containers)} containers with unique ports")
        print(f"   Used host ports: {sorted(used_ports)}")
        return True
        
    except Exception as e:
        print(f"❌ Error testing multiple containers: {e}")
        return False

def test_engines_api(port: str) -> bool:
    """Test the engines API endpoint."""
    print("\n📋 Testing engines API...")
    
    try:
        response = requests.get(f'http://localhost:{port}/engines', timeout=10)
        if response.status_code == 200:
            engines = response.json()
            print(f"✅ Engines API accessible")
            print(f"   Found {len(engines)} engines")
            
            for i, engine in enumerate(engines):
                print(f"   Engine {i+1}: {engine.get('container_id', 'N/A')[:12]} - Port: {engine.get('port', 'N/A')}")
            
            return True
        else:
            print(f"❌ Engines API failed: {response.status_code}")
            return False
            
    except Exception as e:
        print(f"❌ Error testing engines API: {e}")
        return False

def test_generic_vs_acestream_endpoints(port: str, api_key: str) -> bool:
    """Test that acestream endpoint is different from generic provision endpoint."""
    print("\n📋 Testing generic vs acestream provision endpoints...")
    
    headers = {
        'Authorization': f'Bearer {api_key}',
        'Content-Type': 'application/json'
    }
    
    # Test generic endpoint
    generic_data = {
        'image': 'hello-world',
        'env': {},
        'labels': {'test': 'generic'},
        'ports': {}
    }
    
    try:
        response = requests.post(
            f'http://localhost:{port}/provision',
            headers=headers,
            json=generic_data,
            timeout=30
        )
        
        if response.status_code == 200:
            generic_result = response.json()
            print(f"✅ Generic provision works")
            print(f"   Response: {generic_result}")
            
            # Generic should return just container_id
            if 'container_id' not in generic_result:
                print(f"❌ Generic provision missing container_id")
                return False
                
            # Should not have acestream-specific fields
            acestream_fields = ['host_http_port', 'container_http_port', 'container_https_port']
            for field in acestream_fields:
                if field in generic_result:
                    print(f"❌ Generic provision should not have acestream field: {field}")
                    return False
        else:
            print(f"❌ Generic provision failed: {response.status_code}")
            return False
    
    except Exception as e:
        print(f"❌ Error testing generic provision: {e}")
        return False
    
    # Test acestream endpoint
    acestream_data = {
        'labels': {'test': 'acestream'},
        'env': {},
        'image': 'hello-world'
    }
    
    try:
        response = requests.post(
            f'http://localhost:{port}/provision/acestream',
            headers=headers,
            json=acestream_data,
            timeout=30
        )
        
        if response.status_code == 200:
            acestream_result = response.json()
            print(f"✅ Acestream provision works")
            
            # Acestream should have specific fields
            required_fields = ['container_id', 'host_http_port', 'container_http_port', 'container_https_port']
            for field in required_fields:
                if field not in acestream_result:
                    print(f"❌ Acestream provision missing required field: {field}")
                    return False
            
            print(f"   Has all required acestream fields: {required_fields}")
            return True
        else:
            print(f"❌ Acestream provision failed: {response.status_code}")
            return False
    
    except Exception as e:
        print(f"❌ Error testing acestream provision: {e}")
        return False

def cleanup_containers(label_filter: str):
    """Clean up test containers."""
    print("\n🧹 Cleaning up test containers...")
    
    try:
        client = docker.from_env()
        containers = client.containers.list(all=True, filters={'label': label_filter})
        
        for container in containers:
            try:
                print(f"   Removing container: {container.id[:12]}")
                container.stop(timeout=5)
                container.remove()
            except Exception as e:
                print(f"   Error removing {container.id[:12]}: {e}")
        
        print(f"✅ Cleaned up {len(containers)} containers")
        
    except Exception as e:
        print(f"⚠️ Error during cleanup: {e}")

def main():
    """Main test function."""
    print("🧪 Testing acestream orchestrator integration...")
    
    # Setup environment
    test_env = setup_test_environment()
    port = test_env['APP_PORT']
    api_key = test_env['API_KEY']
    
    proc = None
    try:
        # Start orchestrator
        proc = start_orchestrator(test_env)
        
        # Wait for ready
        if not wait_for_orchestrator(port):
            print("❌ Orchestrator failed to start")
            return False
        
        # Run tests
        tests = [
            lambda: test_engines_api(port),
            lambda: test_acestream_provision_endpoint(port, api_key),
            lambda: test_multiple_unique_ports(port, api_key, 3),
            lambda: test_generic_vs_acestream_endpoints(port, api_key),
        ]
        
        results = []
        for i, test in enumerate(tests):
            try:
                result = test()
                results.append(result)
                if not result:
                    print(f"❌ Test {i+1} failed")
                else:
                    print(f"✅ Test {i+1} passed")
            except Exception as e:
                print(f"❌ Test {i+1} error: {e}")
                results.append(False)
        
        # Final summary
        passed = sum(results)
        total = len(results)
        
        print(f"\n📊 Test Summary: {passed}/{total} tests passed")
        
        if passed == total:
            print("🎉 All tests passed! Acestream orchestrator integration is working correctly.")
            return True
        else:
            print("❌ Some tests failed.")
            return False
        
    finally:
        # Cleanup
        if proc:
            print("\n🛑 Stopping orchestrator...")
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
        
        cleanup_containers(test_env['CONTAINER_LABEL'])

if __name__ == "__main__":
    success = main()
    sys.exit(0 if success else 1)