drop database if exists `simulator`;
create database `simulator`;
use `simulator`;
create table t (id int, name varchar(20));

drop database if exists `simulator_2`;
create database `simulator_2`;
use `simulator_2`;
create table simulate_1 (id int, name varchar(20));
create table simulate_2 (id int, name varchar(20));
create table simulate_3 (id int, name varchar(20));