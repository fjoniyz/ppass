# Private PaaS
The main idea of this project is to create a platform, for deploying new services in the homelab. 
While we can achieve this also using Kubernetes, we want to learn more about Linux and this is why we have the homelab. 

## High level components
There will be a node which holds the Nginx load balancer and other nodes which will have the respective workloads. To achieve this architecture, we need the following high level components:
1. Data plane node
2. Control plane node(s)

### Data plane node
The data plane node has the load balancer and controls the cluster. 
Controlling the cluster here means to do the following:

1. Manage each workload running in the cluster
2. Send instructions to the nodes
3. Run the Nginx load balancer

The Nginx load balancer is run as a simple "container"(in this project a container is a process which we control ourselves and not a container which gets created by Docker or Linux automatically). 

How each of these components works in detail I will explain further below.

### Control plane node
The control plane node has the workloads. 
Workloads are controlled by a process that runs on the node and communicates with the other process running in the data plane node using networking protocols. 
Tasks of the control plane node:

1. Run the workloads
2. Execute the instructions coming from the data plane process and inform the process accordingly

## Low level design

In this section I will explain how the in-depth architecture should look like for each of the components we will have. 

### Control plane process
The process will use a KV database(similar to what k8s uses with etcd) to save the node where the workload is running. 
This is then used to send requests to respective nodes. 
For the KV store, I would simply use Redis just because I know how to work with it best and it's not really that important for this project to go into deeper details of this. 

The process will also send requests to the other node regarding the workloads.
This part of communication is trivial, as the process will actually use the networking namespace. 
For the load balancer the communication gets more complicated. 
The load balancer will actually use its own networking namespace. The requests will then need to actually travel via a network bridge from the network workspace of the load balancer process, to the network namespace of the host, then through the local network to the node. 
The node will receive the request and the same bridge connection logic exists also in the node, between the host and the workloads. 

One can think of a single workload, similar to a pod in Kubernetes. 
They don't share however their namespace resources with other processes or containers contrary to what can happen in a pod. 

### Load balancer
The load balancer running on the data plane, has the task of distributing the requests to the specific workloads. 
It's an Nginx load balancer and we need to make sure that for each new workload, there is no collision of logic between them. 
Since we are using only one other node at the moment, this is not a big issue since there cannot be two processes listening to the same port however for the future we need to keep this in mind.

### Data plane processes 
The specific application workloads will be running in the data plane nodes. 
These are the more complicated processes to isolate, simply because of the security and performance aspect. 
Each process will have its own network namespace, filesystem namespace, and so on. The specific namespaces will need to be defined but up until now, I know that the filesystem and the networking namespace will for sure be separated. 
For the rest, I will make decisions with time.

I differentiate between two main types of workloads: services and databases. 
In the first phases of this, I will make sure that the services are communicating only with other workloads in the same machine. 
To do this, we will need bridges between different network namespaces to be created. 
In the future we can add support for distributed systems communication between two nodes when I get another one in my cluster. It should be similar to what load balancer -> workload communication is doing but I have to figure out the service discovery part. 

Inside of the processes, I can use the Alpine file system. I can first download it using this command `curl -o alpine.tar.gz https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/$(uname -m)/alpine-minirootfs-3.22.1-$(uname -m).tar.gz`
and then chroot to that filesystem for each process.
